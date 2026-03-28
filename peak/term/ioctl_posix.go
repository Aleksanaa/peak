//go:build linux || darwin || dragonfly || solaris || openbsd || netbsd || freebsd

package terminal

import (
	"golang.org/x/sys/unix"
)

func (t *VT) ptyResize() error {
	if t.pty == nil {
		return nil
	}
	w := &unix.Winsize{
		Row:    uint16(t.dest.rows),
		Col:    uint16(t.dest.cols),
		Xpixel: 16 * uint16(t.dest.cols),
		Ypixel: 16 * uint16(t.dest.rows),
	}
	return unix.IoctlSetWinsize(int(t.pty.Fd()), unix.TIOCSWINSZ, w)
}
