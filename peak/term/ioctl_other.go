//go:build plan9 || nacl || windows

package terminal

func (t *VT) ptyResize() error {
	return nil
}
