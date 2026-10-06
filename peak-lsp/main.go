package main

import (
	"flag"
	"log"
	"sync"

	"github.com/aleksana/peak/internal/peakfs"
	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func main() {
	socket := flag.String("s", peakfs.Socket(), "peak 9P socket")
	flag.Parse()
	fs, err := vfs.NewNinePClientFs("unix", *socket)
	if err != nil {
		log.Fatalf("connect to peak: %v", err)
	}
	log.Printf("connected to peak at %s", *socket)
	watchEvents(fs)
}

// watchEvents opens /event and blocks on it, starting a watchWindow goroutine
// for each "new <id>" line. "get <id> <filename>" and "put <id> <filename>"
// lines are forwarded to the corresponding window's retitle channel so it can
// re-detect the language after a file change.
func watchEvents(fs afero.Fs) {
	var mu sync.Mutex
	retitleChans := make(map[int]chan<- string)

	start := func(id int) {
		mu.Lock()
		_, already := retitleChans[id]
		var ch chan string
		if !already {
			ch = make(chan string, 4)
			retitleChans[id] = ch
		}
		mu.Unlock()
		if !already {
			go func() {
				watchWindow(fs, id, ch)
				mu.Lock()
				delete(retitleChans, id)
				mu.Unlock()
			}()
		}
	}

	retitle := func(id int, filename string) {
		mu.Lock()
		ch := retitleChans[id]
		mu.Unlock()
		if ch != nil {
			select {
			case ch <- filename:
			default:
				// channel full; drop — next event will carry the latest name
			}
		}
	}

	err := peakfs.Watch(fs, func(ev peakfs.Event) {
		switch ev.Kind {
		case "new":
			start(ev.ID)
		case "get", "put":
			if ev.Name != "" {
				retitle(ev.ID, ev.Name)
			}
		}
	})
	if err != nil {
		log.Fatalf("watch peak events: %v", err)
	}
}
