package session

import (
	"fmt"

	"github.com/aleksanaa/peak/internal/vfs/afero"
)

// RemoteSession implements Session over VFS files from a mounted filesystem's
// /new protocol. IoRead and IoWrite are separate handles on the same io file
// so their 9P offsets are tracked independently, reads advance the read
// position, writes advance the write position, and neither interferes with
// the other.
type RemoteSession struct {
	IoRead  afero.File
	IoWrite afero.File
	Ctl     afero.File
}

func NewRemote(ioRead, ioWrite, ctl afero.File) *RemoteSession {
	return &RemoteSession{IoRead: ioRead, IoWrite: ioWrite, Ctl: ctl}
}

func (s *RemoteSession) Read(p []byte) (int, error)  { return s.IoRead.Read(p) }
func (s *RemoteSession) Write(p []byte) (int, error) { return s.IoWrite.Write(p) }
func (s *RemoteSession) Resize(rows, cols int) error {
	_, err := fmt.Fprintf(s.Ctl, "resize %dx%d\n", cols, rows)
	return err
}
func (s *RemoteSession) Close() error {
	fmt.Fprint(s.Ctl, "kill\n")
	s.Ctl.Close()
	s.IoWrite.Close()
	return s.IoRead.Close()
}
