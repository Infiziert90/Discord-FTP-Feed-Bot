package main

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/dustin/go-humanize"
	"github.com/jlaffaye/ftp"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type File struct {
	Name string
	Size uint64
	Path string
}

type Folder struct {
	Name    string
	Path    string
	Entries []*File
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

var (
	config      Config
	firstTime   = true
	session     *discordgo.Session
	alreadySeen = make(map[string]bool)
)

func init() {
	createConfig(&config)

	if config.BotToken == "YOUR_BOT_TOKEN" {
		panic("Default BotToken, pls change the settings in config.yaml.")
	}
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
		fmt.Printf("Start filling list with all entries.\n")
		scanFTP(false)
		fmt.Printf("Finished.\n")
		go RunForEver()
		fmt.Printf("Run forever.\n")
		firstTime = false
	}
}

func RunForEver() {
	for {
		time.Sleep(10 * time.Minute)
		fmt.Printf("Scan FTP\n")
		start := time.Now()
		embed := scanFTP(true)
		sendMessage(embed)
		elapsed := time.Since(start)
		fmt.Printf("Finished scan   Time: %s\n", elapsed)
	}
}

func sendMessage(embed *discordgo.MessageEmbed) {
	if len(embed.Fields) == 0 {
	} else if len(embed.Fields) > 25 {
		embed.Fields = embed.Fields[:10]
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Truncated", Value: "Truncated"})
		session.ChannelMessageSendEmbed(config.Channel, embed)
	} else {
		session.ChannelMessageSendEmbed(config.Channel, embed)
	}
}

func observeUpload(client *ftp.ServerConn, uploadCache map[string]*File) {
	defer client.Logout()
	for len(uploadCache) > 0 {
		time.Sleep(20 * time.Second)

		embed := discordgo.MessageEmbed{Title: "FTP"}
		Finished := make(map[string]*File)
		for k, v := range uploadCache {
			newSize, err := client.FileSize(v.Path + v.Name)
			if err != nil {
				fmt.Println(err)
				fmt.Printf("Deleted %s\n", v.Name)
				delete(uploadCache, v.Path+v.Name)
				continue
			}

			if v.Size != uint64(newSize) {
				v.Size = uint64(newSize)
				continue
			}

			Finished[v.Name] = v
			delete(uploadCache, v.Path+v.Name)
			fmt.Printf("Finished upload: %s\n", k)
		}

		for _, v := range Finished {
			value := fmt.Sprintf("Path: %s   Size: %s\n", v.Path, humanize.Bytes(v.Size))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v.Name, Value: value})
		}
		sendMessage(&embed)
	}
}

func findNewFiles(client *ftp.ServerConn, entries map[string]*Folder, ftpEntries []*ftp.Entry, path string, uploadCache map[string]*File) {
	for i := range ftpEntries {
		if alreadySeen[path+ftpEntries[i].Name] || config.Exclude[ftpEntries[i].Name] {
			continue
		}

		if ftpEntries[i].Type == 1 {
			fullPath := path + ftpEntries[i].Name + "/"
			entries[fullPath] = &Folder{ftpEntries[i].Name, fullPath, make([]*File, 0)}
			newFolder, err := client.List(fullPath)
			if err == nil {
				findNewFiles(client, entries, newFolder, fullPath, uploadCache)
			}
		} else {
			uploadCache[path+ftpEntries[i].Name] = &File{ftpEntries[i].Name, ftpEntries[i].Size, path}
			alreadySeen[path+ftpEntries[i].Name] = true
		}
	}
}

func checkFiles(client *ftp.ServerConn, entries map[string]*Folder, uploadCache map[string]*File, normalRun bool) {
	for _, v := range uploadCache {
		if normalRun {
			newSize, _ := client.FileSize(v.Path + v.Name)
			if v.Size != uint64(newSize) {
				continue
			}
			fmt.Printf("Found new file: %s\n", v.Name)
		}
		entries[v.Path].Entries = append(entries[v.Path].Entries, v)
		delete(uploadCache, v.Path+v.Name)
	}
}

func scanFTP(normalRun bool) (*discordgo.MessageEmbed) {
	uploadCache := make(map[string]*File)
	embed := discordgo.MessageEmbed{Title: "FTP", Description: "  "}
	entries := map[string]*Folder{"/": {"/", "/", make([]*File, 0)}}
	client, err := ftp.Dial(config.IP + ":" + config.Port)
	if err != nil {
		fmt.Println(err)
		return &embed
	}

	if err := client.Login(config.User, config.PW); err != nil {
		fmt.Println(err)
		return &embed
	}

	ftpEntries, err := client.List("/")
	if err != nil {
		fmt.Println(err)
		return &embed
	}

	findNewFiles(client, entries, ftpEntries, "/", uploadCache)
	time.Sleep(1 * time.Second)
	checkFiles(client, entries, uploadCache, normalRun)

	for _, v := range entries {
		if len(v.Entries) == 0 {
		} else if len(v.Entries) == 1 {
			value := fmt.Sprintf("Path: %s   Size: %s", v.Path, humanize.Bytes(v.Entries[0].Size))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v.Entries[0].Name, Value: value})
		} else {
			value := fmt.Sprintf("%d new episodes", len(v.Entries))
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: v.Path, Value: value})
		}
	}

	go observeUpload(client, uploadCache)
	return &embed
}
