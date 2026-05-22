package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
	watchEvents(fs)
}

// watchEvents opens /event and blocks on it, starting a watchWindow goroutine
// for each "new <id>" line. "close <id>" lines are informational — watchWindow
// exits naturally when the window's event file reaches EOF.
func watchEvents(fs afero.Fs) {
	var mu sync.Mutex
	watching := make(map[int]bool)

	start := func(id int) {
		mu.Lock()
		already := watching[id]
		if !already {
			watching[id] = true
		}
		mu.Unlock()
		if !already {
			go func() {
				watchWindow(fs, id)
				mu.Lock()
				delete(watching, id)
				mu.Unlock()
			}()
		}
	}

	eventF, err := fs.Open("/event")
	if err != nil {
		log.Fatalf("open /event: %v", err)
	}
	defer eventF.Close()

	scanner := bufio.NewScanner(eventF)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "new" {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				start(id)
			}
		}
	}
}
