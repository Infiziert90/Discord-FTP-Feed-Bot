package main

import (
	"fmt"
	"github.com/Infiziert90/goftp"
	"github.com/bwmarrin/discordgo"
	"github.com/dustin/go-humanize"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type FTPConn struct {
	mux        sync.Mutex
	connection *goftp.Client
}

func (c *FTPConn) List(path string) []os.FileInfo {
	c.mux.Lock()
	defer c.mux.Unlock()
	for i := 0; i < 5; i++ {
		entries, err := c.connection.ReadDir(path)
		if err == nil {
			return entries
		}
		time.Sleep(200 * time.Millisecond)
	}

	c.Reconnect()
	entries, err := c.connection.ReadDir(path)
	if err == nil {
		return entries
	}

	return nil
}

func (c *FTPConn) FileSize(path string) os.FileInfo {
	c.mux.Lock()
	defer c.mux.Unlock()
	for i := 0; i < 5; i++ {
		size, err := c.connection.Stat(path)
		if err == nil {
			return size
		}
		time.Sleep(200 * time.Millisecond)
	}

	c.Reconnect()
	size, err := c.connection.Stat(path)
	if err == nil {
		return size
	}

	return nil
}

func (c *FTPConn) Reconnect() {
	err := c.connection.Close()
	if err != nil {
		fmt.Printf("Error while closing the connection: %s\n", err)
	}

	fmt.Println("Creating new FTP client.")
	c.connection = createNewFTPClient(os.Stdout)
}

func (c *FTPConn) Quit() {
	err := c.connection.Close()
	if err != nil {
		fmt.Printf("Error while closing the connection: %s\n", err)
	}
}

type File struct {
	Name string
	Size int64
	Path string
}

type Config struct {
	BotToken string          `yaml:"BotToken"`
	Channel  string          `yaml:"Channel"`
	Exclude  map[string]bool `yaml:"Exclude"`
	IP       string          `yaml:"IP"`
	Port     string          `yaml:"Port"`
	User     string          `yaml:"User"`
	PW       string          `yaml:"PW"`
}

const FTPCONNLIMIT = 6

var (
	config      Config
	firstTime   = true
	session     *discordgo.Session
	alreadySeen sync.Map
	random      *rand.Rand
)

func init() {
	createConfig(&config)

	if config.BotToken == "YOUR_BOT_TOKEN" {
		panic("Default BotToken, pls change the settings in config.yaml.")
	}

	s1 := rand.NewSource(time.Now().UnixNano())
	random = rand.New(s1)
}

func createConfig(conf *Config) {
	yamlFile, err := ioutil.ReadFile("config.yaml")
	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}

// Main
func main() {
	discord, err := discordgo.New("Bot " + config.BotToken)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}
	discord.AddHandler(OnReady)

	err = discord.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	discord.Close()
}

func OnReady(s *discordgo.Session, _ *discordgo.Ready) {
	if firstTime {
		session = s
		fmt.Printf("Start filling already seen list with current entries.\n")
		scanFTP(true)
		fmt.Printf("Finished Filling.\n")
		go RunForEver()
		fmt.Printf("Run forever.\n")
		firstTime = false
	}
}

func RunForEver() {
	for {
		fmt.Println("Sleeping")
		time.Sleep(10 * time.Minute)
		fmt.Printf("Scan FTP\n")
		start := time.Now()
		embed := scanFTP(false)
		sendMessage(embed)
		elapsed := time.Since(start)
		fmt.Printf("Finished scan   Time: %s\n", elapsed)
	}
}

func createNewFTPClient(writer io.Writer) *goftp.Client {
	ftpConfig := goftp.Config{
		User:               config.User,
		Password:           config.PW,
		ConnectionsPerHost: 1,
		Timeout:            3 * time.Second,
		Logger:             writer,
	}

	client, err := goftp.DialConfig(ftpConfig, config.IP+":"+config.Port)
	if err != nil {
		panic(err)
	}

	return client
}

func createFTPPool() [FTPCONNLIMIT]FTPConn {
	var pool [FTPCONNLIMIT]FTPConn
	for i := 0; i < FTPCONNLIMIT; i++ {
		pool[i] = FTPConn{connection: createNewFTPClient(ioutil.Discard)}
	}

	return pool
}

func getRandomConnectionIndex() int {
	return random.Intn(FTPCONNLIMIT)
}

func scanFTP(init bool) *discordgo.MessageEmbed {
	var tmpCache sync.Map
	var entries sync.Map

	pool := createFTPPool()

	embed := discordgo.MessageEmbed{Title: "FTP", Description: "  "}

	ftpEntries := pool[0].List("/")
	if ftpEntries == nil {
		return &embed
	}

	var wg sync.WaitGroup
	wg.Add(1)
	findNewFiles(pool, &tmpCache, ftpEntries, "/", &wg)
	wg.Wait()

	if init {
		return nil
	}

	time.Sleep(1 * time.Second)

	wg.Add(1)
	checkFiles(pool, &tmpCache, &entries, &wg)
	wg.Wait()

	e := make(map[string][]*File, 0)
	entries.Range(func(key interface{}, value interface{}) bool {
		v := value.(*File)
		e[v.Path] = append(e[v.Path], v)

		return true
	})

	for _, v := range e {
		if len(v) == 1 {
			value := fmt.Sprintf("Path: %s   Size: %s", v[0].Path, humanize.Bytes(uint64(v[0].Size)))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v[0].Name, Value: value})
		} else {
			value := fmt.Sprintf("%d new episodes", len(v))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v[0].Path, Value: value})
		}
	}

	fmt.Println("Start observeUpload")
	go observeUpload(pool, &tmpCache)

	return &embed
}

func sendMessage(embed *discordgo.MessageEmbed) {
	if len(embed.Fields) == 0 {
	} else if len(embed.Fields) > 25 {
		embed.Fields = embed.Fields[:20]
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Truncated", Value: "Truncated"})
		session.ChannelMessageSendEmbed(config.Channel, embed)
	} else {
		session.ChannelMessageSendEmbed(config.Channel, embed)
	}
}

func getSyncMapLength(myMap *sync.Map) int {
	length := 0

	myMap.Range(func(_, _ interface{}) bool {
		length++

		return true
	})

	return length
}

func dieWhenStuck(checker *int, key string) {
	time.Sleep(180 * time.Second)
	if *checker != 1 {
		panic("Stuck with path: " + key)
	}
}

func findNewFiles(pool [FTPCONNLIMIT]FTPConn, tmpCache *sync.Map, ftpEntries []os.FileInfo, path string, oldWG *sync.WaitGroup) {
	var newWG sync.WaitGroup
	for i := 0; i < len(ftpEntries); i++ {
		name := ftpEntries[i].Name()
		fullPath := path + name

		if _, ok := alreadySeen.Load(fullPath); !(config.Exclude[name] || ok) {
			if ftpEntries[i].IsDir() {

				checker := 0
				go dieWhenStuck(&checker, fullPath)
				index := getRandomConnectionIndex()
				newFolder := pool[index].List(fullPath + "/")
				checker = 1

				if newFolder != nil {
					newWG.Add(1)
					go findNewFiles(pool, tmpCache, newFolder, fullPath+"/", &newWG)
				}
			} else {
				if !firstTime {
					fmt.Printf("Found new Files: %s\n", fullPath)
				}
				tmpCache.Store(fullPath, &File{name, ftpEntries[i].Size(), path})
				alreadySeen.Store(fullPath, true)
			}
		}
	}

	newWG.Wait()
	oldWG.Done()
}

func checkFiles(pool [FTPCONNLIMIT]FTPConn, tmpCache *sync.Map, entries *sync.Map, oldWG *sync.WaitGroup) {
	var newWG sync.WaitGroup
	tmpCache.Range(func(key interface{}, value interface{}) bool {
		newWG.Add(1)
		go func() {
			v := value.(*File)

			index := getRandomConnectionIndex()
			newSize := pool[index].FileSize(v.Path + v.Name)

			if newSize == nil || v.Size != newSize.Size() {
				newWG.Done()
				return
			}

			fmt.Printf("Found new file: %s\n", v.Name)
			entries.Store(v.Path+v.Name, v)
			tmpCache.Delete(key)

			newWG.Done()
		}()

		return true
	})

	newWG.Wait()
	oldWG.Done()
}

func observeUpload(pool [FTPCONNLIMIT]FTPConn, uploadCache *sync.Map) {
	for length := getSyncMapLength(uploadCache); length > 0; length = getSyncMapLength(uploadCache) {
		time.Sleep(40 * time.Second)

		embed := discordgo.MessageEmbed{Title: "FTP"}
		Finished := make(map[string]File)

		uploadCache.Range(func(key interface{}, value interface{}) bool {
			k := key.(string)
			v := value.(*File)

			index := getRandomConnectionIndex()
			newSize := pool[index].FileSize(v.Path + v.Name)
			if newSize == nil {
				fmt.Printf("Deleted %s\n", v.Name)
				uploadCache.Delete(key)
				alreadySeen.Delete(k)
				return true
			}

			if v.Size != newSize.Size() {
				fmt.Printf("Old size: %d \t New size: %d\n", v.Size, newSize.Size())
				v.Size = newSize.Size()
				return true
			}

			Finished[v.Name] = *v
			uploadCache.Delete(key)
			fmt.Printf("Finished upload: %s\n", v.Name)

			return true
		})

		for _, v := range Finished {
			value := fmt.Sprintf("Path: %s   Size: %s\n", v.Path, humanize.Bytes(uint64(v.Size)))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v.Name, Value: value})
		}

		sendMessage(&embed)
	}

	for index := range pool {
		pool[index].Quit()
	}
}
