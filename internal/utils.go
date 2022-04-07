package internal

import (
	"sync"
	"time"
)

func GetSyncMapLength(myMap *sync.Map) int {
	length := 0

	myMap.Range(func(_, _ interface{}) bool {
		length++

		return true
	})

	return length
}

func Timeout(done *bool, key string) {
	time.Sleep(180 * time.Second)
	if *done {
		return
	}
	panic("Stuck with path: " + key)
}

func GetSortedChannels() []Channel {
	var channels []Channel
	for _, c := range Conf.Channels {
		if c.Regex == "" {
			// ensure that "" is always last
			channels = append(channels, c)
		} else {
			channels = append([]Channel{c}, channels...)
		}
	}

	return channels
}
