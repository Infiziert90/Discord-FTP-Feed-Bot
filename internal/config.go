package internal

import (
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
)

var Conf Config

type Channel struct {
	ID    string `yaml:"ChannelID"`
	Regex string `yaml:"Regex"`
}

type Config struct {
	BotToken string             `yaml:"BotToken"`
	IP       string             `yaml:"IP"`
	Port     string             `yaml:"Port"`
	User     string             `yaml:"User"`
	PW       string             `yaml:"PW"`
	Exclude  map[string]bool    `yaml:"Exclude"`
	Channels map[string]Channel `yaml:"Feeds"`
}

func CreateConfig() {
	yamlFile, err := ioutil.ReadFile("config.yaml")
	err = yaml.Unmarshal(yamlFile, &Conf)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}
