//go:build !windows

package vfs

import "errors"

func connectedServers() ([]string, error) { return nil, errors.ErrUnsupported }

func serverShares(server string) ([]string, error) { return nil, errors.ErrUnsupported }
