package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/aleksana/peak/internal/vfs"
	"github.com/aleksana/peak/internal/vfs/afero"
)

func main() {
	socketPath := flag.String("s", "", "serve on this Unix socket (omit to post to peak's /srv/ssh)")
	peakSocket := flag.String("p", "", "peak 9P socket (default ~/.peak/9p); required when -s is omitted")
	mountPath := flag.String("m", "/peak/ssh", "auto-mount path in peak's namespace")
	noMount := flag.Bool("M", false, "skip auto-mount; mount manually")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-s socket] [-p peak_socket] [-m mount_path]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	// Connect to peak when: virtual socket mode (-s omitted), auto-mount
	// requested (default unless -M), or user explicitly supplied -p (enables bridging).
	var peakFs afero.Fs
	if *socketPath == "" || (!*noMount && *mountPath != "") || *peakSocket != "" {
		sock := *peakSocket
		if sock == "" {
			home, _ := os.UserHomeDir()
			sock = filepath.Join(home, ".peak", "9p")
		}
		var err error
		peakFs, err = vfs.NewNinePClientFs("unix", sock)
		if err != nil {
			if *socketPath == "" {
				log.Fatalf("connect to peak at %s: %v", sock, err)
			}
			log.Printf("warning: connect to peak: %v (bridging and auto-mount disabled)", err)
			peakFs = nil
		} else {
			log.Printf("connected to peak at %s", sock)
		}
	}

	srv := vfs.NewNinePSrv(newHostFs(NewSftpFs(), peakFs))

	if *socketPath != "" {
		// Real Unix socket mode: listen first so the socket exists before
		// telling peak to mount it.
		os.Remove(*socketPath)
		l, err := net.Listen("unix", *socketPath)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		log.Printf("serving on %s", *socketPath)
		if !*noMount && peakFs != nil {
			mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
			if err != nil {
				log.Printf("warning: open /mount: %v (auto-mount disabled)", err)
			} else {
				if _, err := fmt.Fprintf(mountF, "%s %s\n", *socketPath, *mountPath); err != nil {
					log.Printf("warning: mount failed: %v", err)
				} else {
					log.Printf("mounted at %s", *mountPath)
				}
				mountF.Close()
			}
		}
		srv.ServeListener(l)
		return
	}

	// Virtual socket mode: post to peak's /srv/ssh.
	conn, err := peakFs.OpenFile("/srv/ssh", os.O_RDWR, 0)
	if err != nil {
		log.Fatalf("open /srv/ssh: %v", err)
	}

	done := make(chan struct{})
	go func() {
		srv.ServeConn(conn)
		close(done)
	}()

	if !*noMount {
		mountF, err := peakFs.OpenFile("/mount", os.O_WRONLY, 0)
		if err != nil {
			log.Fatalf("open /mount: %v", err)
		}
		fmt.Fprintf(mountF, "/srv/ssh %s\n", *mountPath)
		mountF.Close()
		log.Printf("mounted at %s", *mountPath)
	} else {
		log.Printf("serving on /srv/ssh (to mount: write '/srv/ssh <path>' to peak's /mount)")
	}

	<-done
}
