package vfs

import (
	"runtime"
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
)

const (
	resourceConnected      = 1
	resourceGlobalNet      = 2
	resourceRemembered     = 3
	resourceTypeDisk       = 1
	resourceUsageContainer = 2
)

// netResource is NETRESOURCEW.
type netResource struct {
	Scope, Type, DisplayType, Usage          uint32
	LocalName, RemoteName, Comment, Provider *uint16
}

// connectedServers returns the servers of the shares this user is connected
// to or has saved connections to, as mapped drives are.
func connectedServers() ([]string, error) {
	var servers []string
	for _, scope := range []uintptr{resourceConnected, resourceRemembered} {
		remotes, err := enumResources(scope, nil)
		if err != nil {
			return nil, err
		}
		for _, r := range remotes {
			server, _, _ := strings.Cut(strings.TrimPrefix(r, `\\`), `\`)
			if server != "" && !containsFold(servers, server) {
				servers = append(servers, server)
			}
		}
	}
	return servers, nil
}

// serverShares returns the disk shares of server, from whichever network
// provider serves it: SMB, WSL, WebDAV. Hidden shares (C$) are not listed.
func serverShares(server string) ([]string, error) {
	name, err := windows.UTF16PtrFromString(`\\` + server)
	if err != nil {
		return nil, err
	}
	remotes, err := enumResources(resourceGlobalNet, &netResource{
		Scope: resourceGlobalNet, Type: resourceTypeDisk, Usage: resourceUsageContainer, RemoteName: name,
	})
	if err != nil {
		return nil, err
	}
	for i, r := range remotes {
		remotes[i] = r[strings.LastIndex(r, `\`)+1:]
	}
	return remotes, nil
}

// enumResources returns the remote names of the disk resources of scope, in
// container if it is not nil.
func enumResources(scope uintptr, container *netResource) ([]string, error) {
	var h windows.Handle
	r, _, _ := procWNetOpenEnum.Call(scope, resourceTypeDisk, 0,
		uintptr(unsafe.Pointer(container)), uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	// Windows is handed only container's address: keep it until the
	// enumeration is closed.
	defer runtime.KeepAlive(container)
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
