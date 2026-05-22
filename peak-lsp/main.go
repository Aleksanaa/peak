package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func main() {
	socket := flag.String("s", "", "peak 9P socket (default: ~/.peak/9p)")
	flag.Parse()
	if *socket == "" {
		home, _ := os.UserHomeDir()
		*socket = filepath.Join(home, ".peak", "9p")
	}
	fs, err := vfs.NewNinePClientFs("unix", *socket)
	if err != nil {
		log.Fatalf("connect to peak: %v", err)
	}
	log.Printf("connected to peak at %s", *socket)
	poll(fs)
}

func poll(fs afero.Fs) {
	var mu sync.Mutex
	watching := make(map[int]bool)

	for {
		entries, err := afero.ReadDir(fs, "/")
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			mu.Lock()
			already := watching[id]
			if !already {
				watching[id] = true
			}
			mu.Unlock()
			if !already {
				go func(id int) {
					watchWindow(fs, id)
					mu.Lock()
					delete(watching, id)
					mu.Unlock()
				}(id)
			}
		}
		time.Sleep(time.Second)
	}
}
