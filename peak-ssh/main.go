package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/aleksana/peak/internal/vfs"
)

func main() {
	var (
		socketPath = flag.String("s", "", "Unix socket path to listen on")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s -s socket_path [initial_host]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *socketPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	fs := NewSftpFs()
	if flag.NArg() > 0 {
		host := flag.Arg(0)
		_, err := fs.getClient(host)
		if err != nil {
			log.Printf("Warning: failed to connect to initial host %s: %v", host, err)
		} else {
			log.Printf("Connected to initial host %s", host)
		}
	}

	srv := vfs.NewNinePSrv(fs)
	os.Remove(*socketPath)

	log.Printf("Starting SFTP multiplexer 9P server on %s", *socketPath)
	if err := srv.Serve("unix", *socketPath); err != nil {
		log.Fatalf("9P server error: %v", err)
	}
}
