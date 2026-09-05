//go:build !linux && !darwin && !dragonfly && !solaris && !openbsd && !netbsd && !freebsd

package session

import "errors"

func NewLocal(cmdStr, dir string, env []string) (Session, error) {
	return nil, errors.New("local sessions not supported on this platform")
}
