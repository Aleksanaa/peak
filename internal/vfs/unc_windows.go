package vfs

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	mpr                  = windows.NewLazySystemDLL("mpr.dll")
	procWNetOpenEnum     = mpr.NewProc("WNetOpenEnumW")
	procWNetEnumResource = mpr.NewProc("WNetEnumResourceW")
	procWNetCloseEnum    = mpr.NewProc("WNetCloseEnum")
	procNetShareEnum     = windows.NewLazySystemDLL("netapi32.dll").NewProc("NetShareEnum")
)

// netResource is NETRESOURCEW.
type netResource struct {
	Scope, Type, DisplayType, Usage          uint32
	LocalName, RemoteName, Comment, Provider *uint16
}

// shareInfo1 is SHARE_INFO_1.
type shareInfo1 struct {
	Name   *uint16
	Type   uint32
	Remark *uint16
}

// connectedServers returns the servers of the shares this user is connected
// to or has saved connections to, as mapped drives are.
func connectedServers() ([]string, error) {
	const (
		resourceConnected  = 1
		resourceRemembered = 3
		resourceTypeDisk   = 1
	)
	var servers []string
	seen := map[string]bool{}
	for _, scope := range []uintptr{resourceConnected, resourceRemembered} {
		remotes, err := enumResources(scope, resourceTypeDisk)
		if err != nil {
			return nil, err
		}
		for _, r := range remotes {
			server, _, _ := strings.Cut(strings.TrimPrefix(r, `\\`), `\`)
			if server != "" && !seen[strings.ToLower(server)] {
				seen[strings.ToLower(server)] = true
				servers = append(servers, server)
			}
		}
	}
	return servers, nil
}

// enumResources returns the remote names of the network resources of scope.
func enumResources(scope, typ uintptr) ([]string, error) {
	var h windows.Handle
	if r, _, _ := procWNetOpenEnum.Call(scope, typ, 0, 0, uintptr(unsafe.Pointer(&h))); r != 0 {
		return nil, syscall.Errno(r)
	}
	defer procWNetCloseEnum.Call(uintptr(h))

	var names []string
	buf := make([]byte, 16<<10)
	for {
		count, size := uint32(0xFFFFFFFF), uint32(len(buf))
		r, _, _ := procWNetEnumResource.Call(uintptr(h), uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		switch syscall.Errno(r) {
		case 0:
		case windows.ERROR_NO_MORE_ITEMS:
			return names, nil
		case windows.ERROR_MORE_DATA:
			buf = make([]byte, size)
			continue
		default:
			return nil, syscall.Errno(r)
		}
		for _, nr := range unsafe.Slice((*netResource)(unsafe.Pointer(&buf[0])), count) {
			if nr.RemoteName != nil {
				names = append(names, windows.UTF16PtrToString(nr.RemoteName))
			}
		}
	}
}

// serverShares returns the disk shares of server, leaving out the
// administrative ones (C$, ADMIN$).
func serverShares(server string) ([]string, error) {
	const (
		maxPreferredLength = 0xFFFFFFFF
		stypeMask          = 0xFF
		stypeDisktree      = 0
		stypeSpecial       = 0x80000000
	)
	name, err := windows.UTF16PtrFromString(`\\` + server)
	if err != nil {
		return nil, err
	}
	var (
		buf         *byte
		read, total uint32
	)
	r, _, _ := procNetShareEnum.Call(uintptr(unsafe.Pointer(name)), 1, uintptr(unsafe.Pointer(&buf)),
		maxPreferredLength, uintptr(unsafe.Pointer(&read)), uintptr(unsafe.Pointer(&total)), 0)
	if buf != nil {
		defer windows.NetApiBufferFree(buf)
	}
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	var shares []string
	for _, s := range unsafe.Slice((*shareInfo1)(unsafe.Pointer(buf)), read) {
		if s.Type&stypeMask == stypeDisktree && s.Type&stypeSpecial == 0 {
			shares = append(shares, windows.UTF16PtrToString(s.Name))
		}
	}
	return shares, nil
}
