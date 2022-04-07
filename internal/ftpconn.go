package internal

import (
	"fmt"
	"github.com/Infiziert90/goftp"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"sync"
	"time"
)

const FTPConnLIMIT = 6

var Random *rand.Rand

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

func createNewFTPClient(writer io.Writer) *goftp.Client {
	ftpConfig := goftp.Config{
		User:               Conf.User,
		Password:           Conf.PW,
		ConnectionsPerHost: 1,
		Timeout:            3 * time.Second,
		Logger:             writer,
	}

	client, err := goftp.DialConfig(ftpConfig, Conf.IP+":"+Conf.Port)
	if err != nil {
		panic(err)
	}

	return client
}

func CreateFTPPool() [FTPConnLIMIT]FTPConn {
	var pool [FTPConnLIMIT]FTPConn
	for i := 0; i < FTPConnLIMIT; i++ {
		pool[i] = FTPConn{connection: createNewFTPClient(ioutil.Discard)}
	}

	return pool
}

func GetRandomConnectionIndex() int {
	return Random.Intn(FTPConnLIMIT)
}
