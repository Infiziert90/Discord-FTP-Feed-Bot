package main

import (
	"fmt"
	"github.com/Infiziert90/Discord-FTP-Feed-Bot/internal"
	"github.com/bwmarrin/discordgo"
	"github.com/dustin/go-humanize"
	"math/rand"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"
)

type File struct {
	Name string
	Size int64
	Path string
}

var (
	firstTime   = true
	session     *discordgo.Session
	alreadySeen sync.Map
)

func init() {
	internal.CreateConfig()

	if internal.Conf.BotToken == "YOUR_BOT_TOKEN" {
		panic("Default BotToken, pls change the settings in config.yaml.")
	}

	s1 := rand.NewSource(time.Now().UnixNano())
	internal.Random = rand.New(s1)
}

// Main
func main() {
	discord, err := discordgo.New("Bot " + internal.Conf.BotToken)
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
		fmt.Printf("Init cache list\n")
		scanFTP()
		go RunForEver()
		fmt.Printf("Init done\n")
		firstTime = false
	}
}

func RunForEver() {
	for {
		fmt.Println("Sleeping for 15 min")
		time.Sleep(15 * time.Minute)
		fmt.Printf("Scan FTP\n")
		start := time.Now()
		scanFTP()
		elapsed := time.Since(start)
		fmt.Printf("Finished scan   Time: %s\n", elapsed)
	}
}

func createEmbed(newFolders *map[string][]*File, channel internal.Channel) discordgo.MessageEmbed {
	r, _ := regexp.Compile(channel.Regex)
	embed := discordgo.MessageEmbed{Title: "FTP", Description: "  "}

	for k, v := range *newFolders {
		if r.MatchString(v[0].Path) {
			delete(*newFolders, k)

			if len(v) == 1 {
				value := fmt.Sprintf("Path: %s   Size: %s", v[0].Path, humanize.Bytes(uint64(v[0].Size)))
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v[0].Name, Value: value})
			} else {
				value := fmt.Sprintf("%d new episodes", len(v))
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v[0].Path, Value: value})
			}
		}
	}

	return embed
}

func sendMessage(embed discordgo.MessageEmbed, channel internal.Channel) {
	if len(embed.Fields) == 0 {
		return
	} else if len(embed.Fields) > 25 {
		embed.Fields = embed.Fields[:23]
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Truncated", Value: "Truncated"})
	}
	session.ChannelMessageSendEmbed(channel.ID, &embed)
}

func scanFTP() {
	var tmpCache sync.Map
	var entries sync.Map

	pool := internal.CreateFTPPool()

	ftpEntries := pool[0].List("/")
	if ftpEntries == nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	findNewFiles(pool, &tmpCache, ftpEntries, "/", &wg)
	wg.Wait()

	if firstTime {
		return
	}

	time.Sleep(1 * time.Second)

	wg.Add(1)
	checkFiles(pool, &tmpCache, &entries, &wg)
	wg.Wait()

	newFolders := make(map[string][]*File, 0)
	entries.Range(func(key interface{}, value interface{}) bool {
		v := value.(*File)
		newFolders[v.Path] = append(newFolders[v.Path], v)

		return true
	})

	fmt.Println("Start observeUpload")
	go observeUpload(pool, &tmpCache)

	for _, channel := range internal.GetSortedChannels() {
		embed := createEmbed(&newFolders, channel)
		sendMessage(embed, channel)
	}
}

func findNewFiles(pool [internal.FTPConnLIMIT]internal.FTPConn, tmpCache *sync.Map, ftpEntries []os.FileInfo, path string, oldWG *sync.WaitGroup) {
	var newWG sync.WaitGroup
	for i := 0; i < len(ftpEntries); i++ {
		name := ftpEntries[i].Name()
		fullPath := path + name

		if _, ok := alreadySeen.Load(fullPath); !(internal.Conf.Exclude[name] || ok) {
			if ftpEntries[i].IsDir() {

				done := false
				go internal.Timeout(&done, fullPath)
				index := internal.GetRandomConnectionIndex()
				newFolder := pool[index].List(fullPath + "/")
				done = true

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

func checkFiles(pool [internal.FTPConnLIMIT]internal.FTPConn, tmpCache *sync.Map, entries *sync.Map, oldWG *sync.WaitGroup) {
	var newWG sync.WaitGroup
	tmpCache.Range(func(key interface{}, value interface{}) bool {
		newWG.Add(1)
		go func() {
			v := value.(*File)

			index := internal.GetRandomConnectionIndex()
			newSize := pool[index].FileSize(v.Path + v.Name)

			if newSize == nil || v.Size != newSize.Size() {
				newWG.Done()
				return
			}

			entries.Store(v.Path+v.Name, v)
			tmpCache.Delete(key)

			newWG.Done()
		}()

		return true
	})

	newWG.Wait()
	oldWG.Done()
}

func observeUpload(pool [internal.FTPConnLIMIT]internal.FTPConn, uploadCache *sync.Map) {
	for length := internal.GetSyncMapLength(uploadCache); length > 0; length = internal.GetSyncMapLength(uploadCache) {
		time.Sleep(40 * time.Second)
		finishedFolders := make(map[string][]*File, 0)

		uploadCache.Range(func(key interface{}, value interface{}) bool {
			k := key.(string)
			v := value.(*File)

			index := internal.GetRandomConnectionIndex()
			newSize := pool[index].FileSize(v.Path + v.Name)
			if newSize == nil {
				fmt.Printf("Deleted %s\n", v.Name)
				uploadCache.Delete(key)
				alreadySeen.Delete(k)
				return true
			}

			if v.Size != newSize.Size() {
				fmt.Printf("%s: Old size: %d \t New size: %d\n", v.Name, v.Size, newSize.Size())
				v.Size = newSize.Size()
				return true
			}

			finishedFolders[v.Name] = append(finishedFolders[v.Path], v)
			uploadCache.Delete(key)
			fmt.Printf("Upload finished: %s\n", v.Name)

			return true
		})

		for _, channel := range internal.GetSortedChannels() {
			embed := createEmbed(&finishedFolders, channel)
			sendMessage(embed, channel)
		}
	}

	for index := range pool {
		pool[index].Quit()
	}
}
