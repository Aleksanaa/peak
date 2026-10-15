package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// A host is checked against ~/.ssh/known_hosts; an unknown one is trusted
// when connected to again.
func TestHostKeys(t *testing.T) {
	signer := func() ssh.Signer {
		_, k, _ := ed25519.GenerateKey(nil)
		s, _ := ssh.NewSignerFromKey(k)
		return s
	}
	ed, other, unknown := signer(), signer(), signer()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaSigner, _ := ssh.NewSignerFromKey(rsaKey)

	home := t.TempDir()
	t.Setenv("HOME", home)
	listen := func() net.Listener {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return l
	}
	known, unseen := listen(), listen()
	knownHosts := filepath.Join(home, ".ssh", "known_hosts")
	os.Mkdir(filepath.Dir(knownHosts), 0700)
	line := knownhosts.Line([]string{knownhosts.Normalize(known.Addr().String())}, ed.PublicKey())
	os.WriteFile(knownHosts, []byte(line+"\n"), 0600)

	s := NewSftpFs()
	dial := func(l net.Listener, keys ...ssh.Signer) error {
		config := &ssh.ServerConfig{NoClientAuth: true}
		for _, k := range keys {
			config.AddHostKey(k)
		}
		go func() {
			if c, err := l.Accept(); err == nil {
				ssh.NewServerConn(c, config)
				c.Close()
			}
		}()
		addr := l.Addr().String()
		check, algos, err := s.hostKeys(addr)
		if err != nil {
			return err
		}
		c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{HostKeyCallback: check, HostKeyAlgorithms: algos})
		if err == nil {
			c.Close()
		}
		return err
	}
	// age makes l's refusal a second older.
	age := func(l net.Listener) {
		if r, ok := s.refused.Load(l.Addr().String()); ok {
			r := r.(refusal)
			r.at = r.at.Add(-time.Second)
			s.refused.Store(l.Addr().String(), r)
		}
	}

	// Go would rather ask for the RSA key, which known_hosts lacks.
	if err := dial(known, rsaSigner, ed); err != nil {
		t.Errorf("known host refused: %v", err)
	}
	if err := dial(known, other); err == nil {
		t.Error("a known host offering another key was reached")
	}

	err := dial(unseen, unknown)
	if err == nil || !strings.Contains(err.Error(), ssh.FingerprintSHA256(unknown.PublicKey())) {
		t.Fatalf("unknown host: %v, want its fingerprint", err)
	}
	if err := dial(unseen, unknown); err == nil {
		t.Error("unknown host trusted at once")
	}
	age(unseen)
	if err := dial(unseen, unknown); err != nil {
		t.Errorf("unknown host connected to again refused: %v", err)
	}
	s = NewSftpFs()
	if err := dial(unseen, unknown); err != nil {
		t.Errorf("trusted host refused: %v", err)
	}

	// With no known_hosts yet, trusting a host makes it.
	t.Setenv("HOME", t.TempDir())
	dial(unseen, unknown)
	age(unseen)
	if err := dial(unseen, unknown); err != nil {
		t.Errorf("with no known_hosts, unknown host connected to again refused: %v", err)
	}
}
