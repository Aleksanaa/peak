package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type localSession struct {
	in  *os.File // writes reach the child's console input
	out *os.File // reads the child's console output

	mu  sync.Mutex
	hpc windows.Handle // the pseudo console; 0 once closed
}

// shellCommand returns the program and command line that run cmdStr with
// the Runner.
func shellCommand(cmdStr string) (app, cmdLine string) {
	app = Runner()
	if base := strings.ToLower(filepath.Base(app)); base == "cmd.exe" || base == "cmd" {
		// cmd parses the rest of its command line itself, unquoted.
		return app, windows.EscapeArg(app) + " /C " + cmdStr
	}
	return app, windows.ComposeCommandLine([]string{app, "-c", cmdStr})
}

// Command returns a command that runs cmdStr, in a console of its own that
// no window shows: a program can't take over peak's.
func Command(cmdStr string) *exec.Cmd {
	app, cmdLine := shellCommand(cmdStr)
	c := exec.Command(app)
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdLine, CreationFlags: windows.CREATE_NO_WINDOW}
	return c
}

// NewLocal starts cmdStr with the Runner (or the Shell if empty) in dir under
// a new ConPTY.
func NewLocal(cmdStr, dir string) (Session, error) {
	var app, cmdLine string
	if cmdStr == "" {
		app = Shell()
		cmdLine = windows.EscapeArg(app)
	} else {
		app, cmdLine = shellCommand(cmdStr)
	}

	// The pseudo console reads the child's input from inR and writes its
	// output to outW; peak holds the other ends.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	// The pseudo console and child keep their own copies of these.
	defer windows.CloseHandle(inR)
	defer windows.CloseHandle(outW)
	s := &localSession{in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}

	if err := windows.CreatePseudoConsole(windows.Coord{X: 80, Y: 24}, inR, outW, 0, &s.hpc); err != nil {
		s.in.Close()
		s.out.Close()
		return nil, err
	}
	pi, err := startAttached(app, cmdLine, dir, s.hpc)
	if err != nil {
		s.Close()
		return nil, err
	}
	windows.CloseHandle(pi.Thread)
	// The pseudo console holds its output pipe open until it is closed, even
	// after the child exits; close it then, so Read sees EOF as on a pty.
	go func() {
		windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		windows.CloseHandle(pi.Process)
		s.closeConsole()
	}()
	return s, nil
}

// startAttached runs cmdLine in dir, attached to the pseudo console hpc.
func startAttached(app, cmdLine, dir string, hpc windows.Handle) (*windows.ProcessInformation, error) {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// The attribute's value is the handle itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return nil, err
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// Null standard handles, so the child can't inherit peak's own console.
	si.Flags = windows.STARTF_USESTDHANDLES

	app16, err := windows.UTF16PtrFromString(app)
	if err != nil {
		return nil, err
	}
	cmd16, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return nil, err
	}
	var dir16 *uint16
	if dir != "" {
		if dir16, err = windows.UTF16PtrFromString(dir); err != nil {
			return nil, err
		}
	}
	pi := new(windows.ProcessInformation)
	err = windows.CreateProcess(app16, cmd16, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		nil, dir16, &si.StartupInfo, pi)
	return pi, err
}

func (s *localSession) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.in.Write(p) }

// closeConsole closes the pseudo console, which ends the programs attached
// to it and closes its end of the output pipe.
func (s *localSession) closeConsole() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hpc != 0 {
		windows.ClosePseudoConsole(s.hpc)
		s.hpc = 0
	}
}

// Close ends the child; a Read blocked meanwhile returns EOF.
func (s *localSession) Close() error {
	s.closeConsole()
	s.in.Close()
	return s.out.Close()
}

func (s *localSession) Resize(rows, cols int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hpc == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(s.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}
