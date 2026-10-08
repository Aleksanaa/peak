// Edit: sam's command language, run as acme runs it.
//
// The parser, the commands and the edit log are ported from Edwood
// (https://github.com/rjkroege/edwood): edit.go, ecmd.go and sam/elog.go,
// which Edwood ported from acme's edit.c, ecmd.c and elog.c. Where Edwood
// strays from acme, as in line addresses and the edit log, this follows
// acme's source in plan9port. Peak's windows stand for acme's files, and the
// state acme keeps in globals while a command runs is an edit's.
//
//	Portions Copyright © 2021 Plan 9 Foundation
//	Portions Copyright © 2001-2008 Russ Cox
//	Portions Copyright © 2008-2009 Google Inc.
//	Portions Copyright © 2017-2021 Google Inc.
//
// The Plan 9 Foundation's code is under the MIT license and Edwood's under
// the BSD 3-Clause license; sregx.LICENSE has both.

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aleksana/peak/internal/quote"
	"github.com/aleksana/peak/peak/regexp"
)

// ---- parse ----

var (
	errBadAddr          = fmt.Errorf("bad address")
	errBadAddrSyntax    = fmt.Errorf("bad address syntax")
	errAddressMissing   = fmt.Errorf("no address")
	errAddrNotRequired  = fmt.Errorf("command takes no address")
	errRegexpMissing    = fmt.Errorf("no regular expression defined")
	errLeftBraceMissing = fmt.Errorf("right brace with no left brace")
	errBadRHS           = fmt.Errorf("bad right hand side")
	errNewlineExpected  = fmt.Errorf("newline expected")
	errDefcmd           = fmt.Errorf("defcmd")
)

type invalidCmdError rune

func (e invalidCmdError) Error() string {
	return fmt.Sprintf("unknown command %c", rune(e))
}

type badDelimiterError rune

func (e badDelimiterError) Error() string {
	return fmt.Sprintf("bad delimiter %c", rune(e))
}

type Range struct {
	q0, q1 int
}

type Addr struct {
	typ  rune // # (char addr), l (line addr), / ? . $ + - , ; " '
	re   string
	left *Addr // left side of , and ;
	num  int
	next *Addr // or right side of , and ;
}

// An Address is a range of a window's body.
type Address struct {
	r Range
	w *Window
}

type Cmd struct {
	addr   *Addr  // address (range of text)
	re     string // regular expression for e.g. 'x'
	cmd    *Cmd   // target of x, g, {, etc.
	text   string // text of a, c, i; rhs of s
	mtaddr *Addr  // address for m, t
	next   *Cmd   // pointer to next element in braces
	num    int
	flag   rune // whatever
	cmdc   rune // command character; 'x' etc.
}

type Cmdtab struct {
	cmdc    rune      // command character
	text    bool      // takes a textual argument?
	regexp  bool      // takes a regular expression?
	addr    bool      // takes an address (m or t)?
	defcmd  rune      // default command; 0==>none
	defaddr Defaddr   // default address
	count   countType // count type (e.g. s can take an unsigned count: s2///)
	token   string    // takes text terminated by one of these
}

type Defaddr int

const (
	aNo Defaddr = iota
	aDot
	aAll
)

type countType int

const (
	cNo countType = iota
	cUnsigned
	cSigned
)

const (
	linex = "\n"
	wordx = " \t\n"
)

var cmdtab = []Cmdtab{
	// cmdc	text	regexp	addr	defcmd	defaddr	count	token
	{'\n', false, false, false, 0, aDot, cNo, ""},
	{'a', true, false, false, 0, aDot, cNo, ""},
	{'b', false, false, false, 0, aNo, cNo, linex},
	{'c', true, false, false, 0, aDot, cNo, ""},
	{'d', false, false, false, 0, aDot, cNo, ""},
	{'e', false, false, false, 0, aNo, cNo, wordx},
	{'f', false, false, false, 0, aNo, cNo, wordx},
	{'g', false, true, false, 'p', aDot, cNo, ""},
	{'i', true, false, false, 0, aDot, cNo, ""},
	{'m', false, false, true, 0, aDot, cNo, ""},
	{'p', false, false, false, 0, aDot, cNo, ""},
	{'r', false, false, false, 0, aDot, cNo, wordx},
	{'s', false, true, false, 0, aDot, cUnsigned, ""},
	{'t', false, false, true, 0, aDot, cNo, ""},
	{'u', false, false, false, 0, aNo, cSigned, ""},
	{'v', false, true, false, 'p', aDot, cNo, ""},
	{'w', false, false, false, 0, aAll, cNo, wordx},
	{'x', false, true, false, 'p', aDot, cNo, ""},
	{'y', false, true, false, 'p', aDot, cNo, ""},
	{'=', false, false, false, 0, aDot, cNo, linex},
	{'B', false, false, false, 0, aNo, cNo, linex},
	{'D', false, false, false, 0, aNo, cNo, linex},
	{'X', false, true, false, 'f', aNo, cNo, ""},
	{'Y', false, true, false, 'f', aNo, cNo, ""},
	{'<', false, false, false, 0, aDot, cNo, linex},
	{'|', false, false, false, 0, aDot, cNo, linex},
	{'>', false, false, false, 0, aDot, cNo, linex},
	/* deliberately unimplemented:
	{'k', false, false, false, 0, aDot, cNo, ""},
	{'n', false, false, false, 0, aNo, cNo, ""},
	{'q', false, false, false, 0, aNo, cNo, ""},
	{'!', false, false, false, 0, aNo, cNo, linex},
	*/
}

// lastpat is the last regular expression given, which an empty one
// stands for.
var lastpat string

type cmdParser struct {
	buf []rune
	pos int
}

func newCmdParser(r []rune) *cmdParser {
	buf := slices.Clone(r)
	if len(buf) == 0 || buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}
	return &cmdParser{buf: buf}
}

func (cp *cmdParser) getch() rune {
	if cp.pos == len(cp.buf) {
		return -1
	}
	c := cp.buf[cp.pos]
	cp.pos++
	return c
}

func (cp *cmdParser) nextc() rune {
	if cp.pos == len(cp.buf) {
		return -1
	}
	return cp.buf[cp.pos]
}

func (cp *cmdParser) ungetch() {
	cp.pos--
	if cp.pos < 0 {
		panic("ungetch")
	}
}

func (cp *cmdParser) getnum(signok bool) int {
	n := 0
	sign := 1
	if signok && cp.nextc() == '-' {
		sign = -1
		cp.getch()
	}
	c := cp.nextc()
	if c < '0' || '9' < c { // no number defaults to 1
		return sign
	}
	for {
		c = cp.getch()
		if !('0' <= c && c <= '9') {
			break
		}
		n = n*10 + int(c-'0')
	}
	cp.ungetch()
	return sign * n
}

func (cp *cmdParser) skipbl() rune {
	var c rune
	for {
		c = cp.getch()
		if !(c == ' ' || c == '\t') {
			break
		}
	}
	if c >= 0 {
		cp.ungetch()
	}
	return c
}

func okdelim(c rune) bool {
	return !(c == '\\' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9'))
}

func (cp *cmdParser) atnl() error {
	cp.skipbl()
	if cp.getch() != '\n' {
		return errNewlineExpected
	}
	return nil
}

func (cp *cmdParser) getrhs(delim rune, cmd rune) (string, error) {
	var s strings.Builder
	var c rune
	for {
		c = cp.getch()
		if !(c > 0 && c != delim && c != '\n') {
			break
		}
		if c == '\\' {
			c = cp.getch()
			if c <= 0 {
				return "", errBadRHS
			}
			if c == '\n' {
				cp.ungetch()
				c = '\\'
			} else if c == 'n' {
				c = '\n'
			} else if c != delim && (cmd == 's' || c != '\\') { // s does its own
				s.WriteRune('\\')
			}
		}
		s.WriteRune(c)
	}
	cp.ungetch() // let client read whether delimiter, '\n' or whatever
	return s.String(), nil
}

func (cp *cmdParser) collecttoken(end string) (string, error) {
	var s strings.Builder
	var c rune
	for {
		c = cp.nextc()
		if c != ' ' && c != '\t' {
			break
		}
		s.WriteRune(cp.getch()) // blanks significant for getname()
	}
	for {
		c = cp.getch()
		if c <= 0 || strings.ContainsRune(end, c) {
			break
		}
		s.WriteRune(c)
	}
	if c != '\n' {
		if err := cp.atnl(); err != nil {
			return "", err
		}
	}
	return s.String(), nil
}

func (cp *cmdParser) collecttext() (string, error) {
	if cp.skipbl() == '\n' {
		cp.getch()
		var s strings.Builder
		for {
			var line strings.Builder
			c := cp.getch()
			for ; c > 0 && c != '\n'; c = cp.getch() {
				line.WriteRune(c)
			}
			if c < 0 {
				s.WriteString(line.String() + "\n")
				break
			}
			if line.String() == "." {
				break
			}
			s.WriteString(line.String() + "\n")
		}
		return s.String(), nil
	}
	delim := cp.getch()
	if !okdelim(delim) {
		return "", badDelimiterError(delim)
	}
	s, err := cp.getrhs(delim, 'a')
	if err != nil {
		return "", err
	}
	if cp.nextc() == delim {
		cp.getch()
	}
	return s, cp.atnl()
}

func cmdlookup(c rune) int {
	for i, cmd := range cmdtab {
		if cmd.cmdc == c {
			return i
		}
	}
	return -1
}

func (cp *cmdParser) parse(nest int) (*Cmd, error) {
	var cmd Cmd
	var err error

	cmd.addr, err = cp.compoundaddr()
	if err != nil {
		return nil, err
	}
	if cp.skipbl() == -1 {
		return nil, nil
	}
	c := cp.getch()
	if c == -1 {
		return nil, nil
	}
	cmd.cmdc = c
	if cmd.cmdc == 'c' && cp.nextc() == 'd' { // sleazy two-character case
		cp.getch() // the 'd'
		cmd.cmdc = 'c' | 0x100
	}
	i := cmdlookup(cmd.cmdc)
	if i >= 0 {
		if cmd.cmdc == '\n' {
			return &cmd, nil // let nl work it all out
		}
		ct := &cmdtab[i]
		if ct.defaddr == aNo && cmd.addr != nil {
			return nil, errAddrNotRequired
		}
		if ct.count != cNo {
			cmd.num = cp.getnum(ct.count == cSigned)
		}
		if ct.regexp {
			// x without pattern . .*\n, indicated by cmd.re==""
			// X without pattern is all files
			c := cp.nextc()
			if ct.cmdc != 'x' && ct.cmdc != 'X' || (c != ' ' && c != '\t' && c != '\n') {
				cp.skipbl()
				c := cp.getch()
				if c == '\n' || c < 0 {
					return nil, errAddressMissing
				}
				if !okdelim(c) {
					return nil, badDelimiterError(c)
				}
				cmd.re, err = cp.getregexp(c)
				if err != nil {
					return nil, err
				}
				if ct.cmdc == 's' {
					cmd.text, err = cp.getrhs(c, 's')
					if err != nil {
						return nil, err
					}
					if cp.nextc() == c {
						cp.getch()
						if cp.nextc() == 'g' {
							cmd.flag = cp.getch()
						}
					}
				}
			}
		}
		if ct.addr {
			cmd.mtaddr, err = cp.simpleaddr()
			if err != nil {
				return nil, err
			}
			if cmd.mtaddr == nil {
				return nil, errBadAddr
			}
		}
		switch {
		case ct.defcmd != 0:
			if cp.skipbl() == '\n' {
				cp.getch()
				cmd.cmd = &Cmd{cmdc: ct.defcmd}
			} else {
				cmd.cmd, err = cp.parse(nest)
				if err != nil {
					return nil, err
				}
				if cmd.cmd == nil {
					return nil, errDefcmd
				}
			}
		case ct.text:
			cmd.text, err = cp.collecttext()
		case len(ct.token) > 0:
			cmd.text, err = cp.collecttoken(ct.token)
		default:
			err = cp.atnl()
		}
		if err != nil {
			return nil, err
		}
	} else {
		switch cmd.cmdc {
		case '{':
			var c, nc *Cmd
			for {
				if cp.skipbl() == '\n' {
					cp.getch()
				}
				nc, err = cp.parse(nest + 1)
				if err != nil {
					return nil, err
				}
				if c != nil {
					c.next = nc
				} else {
					cmd.cmd = nc
				}
				c = nc
				if c == nil {
					break
				}
			}
		case '}':
			if err := cp.atnl(); err != nil {
				return nil, err
			}
			if nest == 0 {
				return nil, errLeftBraceMissing
			}
			return nil, nil
		default:
			return nil, invalidCmdError(cmd.cmdc)
		}
	}
	return &cmd, nil
}

func (cp *cmdParser) getregexp(delim rune) (string, error) {
	var buf strings.Builder
	var c rune
	for {
		c = cp.getch()
		if c == '\\' {
			if cp.nextc() == delim {
				c = cp.getch()
			} else if cp.nextc() == '\\' {
				buf.WriteRune(c)
				c = cp.getch()
			}
		} else if c == delim || c == '\n' {
			break
		}
		buf.WriteRune(c)
	}
	if c != delim && c != 0 {
		cp.ungetch()
	}
	if buf.Len() > 0 {
		lastpat = buf.String()
	}
	if len(lastpat) == 0 {
		return "", errRegexpMissing
	}
	return lastpat, nil
}

func (cp *cmdParser) simpleaddr() (*Addr, error) {
	var addr Addr
	switch cp.skipbl() {
	case '#':
		addr.typ = cp.getch()
		addr.num = cp.getnum(false)
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		addr.typ = 'l'
		addr.num = cp.getnum(false)
	case '/', '?', '"':
		addr.typ = cp.getch()
		var err error
		addr.re, err = cp.getregexp(addr.typ)
		if err != nil {
			return nil, err
		}
	case '.', '$', '+', '-', '\'':
		addr.typ = cp.getch()
	default:
		return nil, nil
	}
	var err error
	addr.next, err = cp.simpleaddr()
	if err != nil {
		return nil, err
	}
	if addr.next != nil {
		switch addr.next.typ {
		case '.', '$', '\'':
			if addr.typ != '"' {
				return nil, errBadAddrSyntax
			}
		case '"':
			return nil, errBadAddrSyntax
		case 'l', '#':
			if addr.typ == '"' {
				break
			}
			fallthrough
		case '/', '?':
			if addr.typ != '+' && addr.typ != '-' {
				// insert the missing '+'
				addr.next = &Addr{typ: '+', next: addr.next}
			}
		}
	}
	return &addr, nil
}

func (cp *cmdParser) compoundaddr() (*Addr, error) {
	var addr Addr
	var err error

	addr.left, err = cp.simpleaddr()
	if err != nil {
		return nil, err
	}
	addr.typ = cp.skipbl()
	if addr.typ != ',' && addr.typ != ';' {
		return addr.left, nil
	}
	cp.getch()
	addr.next, err = cp.compoundaddr()
	if err != nil {
		return nil, err
	}
	next := addr.next
	if next != nil && (next.typ == ',' || next.typ == ';') && next.left == nil {
		return nil, errBadAddrSyntax
	}
	return &addr, nil
}

// ---- run ----

const Enoname = "no file name given"

// An edit is an Edit command running: what acme keeps in globals while it
// runs one.
type edit struct {
	e        *Editor
	col      *Column            // where the command was run, for +Errors
	curtext  *Window            // the current window, which b changes
	addr     Address            // the address of the command running
	nest     int                // how deep in loops the command is
	glooping int                // how deep in X and Y loops
	logs     map[*Window]*elog  // the changes to each window, made at the end
	warned   bool               // of changes out of order
	texts    map[*Window][]rune // each window's text as the command sees it
	clean    map[*Window]bool   // windows read whole from their file
	out      strings.Builder    // what the command prints
}

// An editError ends an Edit command, as acme's editerror ends the thread
// running one.
type editError string

func (e editError) Error() string { return string(e) }

func editerror(format string, args ...any) {
	panic(editError(fmt.Sprintf(format, args...)))
}

// Edit runs cmd in win's body, or with no window for nil. The changes
// commands make are logged and made when all have run, so every address
// refers to the text as it was; if one fails, none are made. What the
// commands print goes to +Errors.
func (e *Editor) Edit(col *Column, win *Window, cmd string) {
	if cmd == "" {
		return
	}
	ed := &edit{
		e: e, col: col, curtext: win,
		logs:  make(map[*Window]*elog),
		texts: make(map[*Window][]rune),
		clean: make(map[*Window]bool),
	}
	if err := ed.run(newCmdParser([]rune(cmd))); err != nil {
		if msg := err.Error(); msg != "" {
			ed.warn("Edit: %s\n", msg)
		}
	} else {
		// update everyone whose edit log has data
		for _, w := range e.allWindows() {
			if l := ed.logs[w]; l != nil && len(*l) > 0 {
				ed.elogapply(w, *l)
				if ed.clean[w] {
					w.markSaved(w.body.GetBuffer().version)
				}
			}
		}
	}
	if ed.out.Len() > 0 {
		e.showError(col, win, ed.out.String())
	}
}

// run runs the commands cp parses until one fails, and returns why.
func (ed *edit) run(cp *cmdParser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			var ok bool
			if err, ok = r.(editError); !ok {
				panic(r)
			}
		}
	}()
	for {
		cmd, err := cp.parse(0)
		if err != nil {
			return err
		}
		if cmd == nil || !ed.exec(ed.curtext, cmd) {
			return nil
		}
	}
}

func (ed *edit) warn(format string, args ...any) {
	fmt.Fprintf(&ed.out, format, args...)
}

// text returns w's text as the command sees it: as it was when the
// command began, or when u last changed it.
func (ed *edit) text(w *Window) []rune {
	t, ok := ed.texts[w]
	if !ok {
		b := w.body.GetBuffer()
		t = b.RunesInRange(0, b.Len())
		ed.texts[w] = t
	}
	return t
}

func (ed *edit) mkaddr(w *Window) Address {
	b := w.body.GetBuffer()
	return Address{Range{b.q0, b.q1}, w}
}

// setdot sets w's dot, which commands that follow start from.
func (ed *edit) setdot(w *Window, r Range) {
	b := w.body.GetBuffer()
	b.q0, b.q1 = r.q0, r.q1
}

func (ed *edit) exec(w *Window, cp *Cmd) bool {
	if w == nil && (cp.addr == nil || cp.addr.typ != '"') &&
		!strings.ContainsRune("bBnqUXY!", cp.cmdc) && // Commands that don't need a window
		!(cp.cmdc == 'D' && len(cp.text) > 0) {
		editerror("no current window")
	}
	i := cmdlookup(cp.cmdc) // will be -1 for '{'
	if i >= 0 && cmdtab[i].defaddr != aNo {
		ap := cp.addr
		if ap == nil && cp.cmdc != '\n' {
			ap = &Addr{typ: '.'}
			cp.addr = ap
			if cmdtab[i].defaddr == aAll {
				ap.typ = '*'
			}
		} else if ap != nil && ap.typ == '"' && ap.next == nil && cp.cmdc != '\n' {
			ap.next = &Addr{typ: '.'}
			if cmdtab[i].defaddr == aAll {
				ap.next.typ = '*'
			}
		}
		if cp.addr != nil { // may be false for '\n'
			if w != nil {
				ed.addr = ed.address(ap, ed.mkaddr(w), 0)
			} else { // a "
				ed.addr = ed.address(ap, Address{}, 0)
			}
			w = ed.addr.w
		}
	}
	switch cp.cmdc {
	case '{':
		var dot Address
		if w != nil {
			dot = ed.mkaddr(w)
		}
		if cp.addr != nil {
			dot = ed.address(cp.addr, dot, 0)
		}
		w = dot.w
		for cp = cp.cmd; cp != nil; cp = cp.next {
			if dot.r.q1 > len(ed.text(w)) {
				editerror("dot extends past end of buffer during { command")
			}
			ed.setdot(w, dot.r)
			ed.exec(w, cp)
		}
		return true
	case '\n':
		return ed.nl(w, cp)
	case 'a':
		return ed.appendx(w, cp, ed.addr.r.q1)
	case 'b':
		return ed.b(cp)
	case 'B':
		return ed.B(w, cp)
	case 'c':
		ed.elogreplace(w, ed.addr.r.q0, ed.addr.r.q1, []rune(cp.text))
		ed.setdot(w, ed.addr.r)
		return true
	case 'd':
		ed.elogdelete(w, ed.addr.r.q0, ed.addr.r.q1)
		ed.setdot(w, Range{ed.addr.r.q0, ed.addr.r.q0})
		return true
	case 'D':
		return ed.D(w, cp)
	case 'e', 'r':
		return ed.e_r(w, cp)
	case 'f':
		ed.cmdname(w, cp.text, true)
		ed.pfilename(w)
		return true
	case 'g', 'v':
		return ed.g(w, cp)
	case 'i':
		return ed.appendx(w, cp, ed.addr.r.q0)
	case 'm', 't':
		return ed.m(w, cp)
	case 'p':
		return ed.pdisplay(w)
	case 's':
		return ed.s(w, cp)
	case 'u':
		return ed.u(w, cp)
	case 'w':
		return ed.w(w, cp)
	case 'x', 'y':
		if cp.re != "" {
			ed.looper(w, cp, cp.cmdc == 'x')
		} else {
			ed.linelooper(w, cp)
		}
		return true
	case 'X', 'Y':
		ed.filelooper(cp, cp.cmdc == 'X')
		return true
	case '=':
		return ed.eq(w, cp)
	case '<', '|', '>':
		ed.runpipe(w, cp.cmdc, cp.text, true)
		return true
	}
	editerror("unknown command %c in cmdexec", cp.cmdc)
	return false
}

// filelist returns the names r gives, or what the command after a < in it
// prints, as quoted fields. None at all, as against blanks, is not ok.
func (ed *edit) filelist(w *Window, r string) (names []string, ok bool) {
	if r == "" {
		return nil, false
	}
	r = strings.TrimLeft(r, " \t")
	if r == "" || r[0] != '<' {
		return quote.Fields(r), true
	}
	// use < command to collect text
	r = ed.runpipe(w, '<', r[1:], false)
	return quote.Fields(r), r != ""
}

func (ed *edit) b(cp *Cmd) bool {
	w := ed.toWindow(cp.text)
	if ed.nest == 0 {
		ed.pfilename(w)
	}
	ed.curtext = w
	return true
}

func (ed *edit) B(w *Window, cp *Cmd) bool {
	names, ok := ed.filelist(w, cp.text)
	if !ok {
		editerror(Enoname)
	}
	if len(names) == 0 {
		ed.e.newFileWindow(ed.e.getTargetColumn(nil, w), w)
	}
	for _, name := range names {
		ed.e.Open(w, name)
	}
	return true
}

// d1 closes w unless it has changes, which it warns about the first time.
func (ed *edit) d1(w *Window) {
	if ed.e.warnDirty(ed.col, w, []*Window{w}) {
		ed.e.RemoveWindow(w)
	}
}

func (ed *edit) D(w *Window, cp *Cmd) bool {
	names, ok := ed.filelist(w, cp.text)
	if !ok {
		ed.d1(w)
		return true
	}
	if len(names) == 0 {
		names = []string{""} // the window with no name
	}
	dir := getwd()
	if w != nil {
		dir = w.GetDir()
	}
	for _, s := range names {
		if s != "" && !filepath.IsAbs(s) {
			s = filepath.Join(dir, s)
		}
		w := ed.lookfile(s)
		if w == nil {
			editerror("no such file %s", s)
		}
		ed.d1(w)
	}
	return true
}

// lookfile returns the window named s, a directory's with or without its
// slash.
func (ed *edit) lookfile(s string) *Window {
	s = strings.TrimSuffix(s, "/")
	for _, w := range ed.e.allWindows() {
		if strings.TrimSuffix(w.fileName(), "/") == s {
			return w
		}
	}
	return nil
}

func (ed *edit) e_r(w *Window, cp *Cmd) bool {
	q0, q1 := ed.addr.r.q0, ed.addr.r.q1
	n := len(ed.text(w))
	if cp.cmdc == 'e' {
		if !ed.e.warnDirty(ed.col, w, []*Window{w}) {
			editerror("") // warnDirty said why
		}
		q0, q1 = 0, n
	}
	allreplaced := q0 == 0 && q1 == n
	name := ed.cmdname(w, cp.text, cp.cmdc == 'e')
	if name == "" {
		editerror(Enoname)
	}
	samename := name == w.fileName()
	var data []byte
	var isdir bool
	var err error
	ed.e.await(func() {
		var fi os.FileInfo
		if fi, err = ns.Stat(name); err == nil {
			if isdir = fi.IsDir(); !isdir {
				data, err = readFile(name)
			}
		}
	})
	if err != nil {
		editerror("can't open %v: %v", name, err)
	}
	if isdir {
		editerror("%v is a directory", name)
	}
	nulls := bytes.IndexByte(data, 0) >= 0
	data = bytes.ReplaceAll(data, []byte{0}, nil)
	ed.elogdelete(w, q0, q1)
	ed.eloginsert(w, q1, []rune(string(data)))
	if nulls {
		ed.warn("%v: NUL bytes elided\n", name)
	} else if allreplaced && samename {
		ed.clean[w] = true
	}
	return true
}

func (ed *edit) g(w *Window, cp *Cmd) bool {
	are := compile(cp.re, "g command")
	m := are.FindForward(ed.text(w), ed.addr.r.q0, ed.addr.r.q1, 1)
	if (m != nil) != (cp.cmdc == 'v') {
		ed.setdot(w, ed.addr.r)
		return ed.exec(w, cp.cmd)
	}
	return true
}

func (ed *edit) copyx(w *Window, addr2 Address) {
	ed.eloginsert(addr2.w, addr2.r.q1, ed.text(w)[ed.addr.r.q0:ed.addr.r.q1])
}

func (ed *edit) move(w *Window, addr2 Address) {
	addr := ed.addr
	if addr.w != addr2.w || addr.r.q1 <= addr2.r.q0 {
		ed.elogdelete(w, addr.r.q0, addr.r.q1)
		ed.copyx(w, addr2)
	} else if addr.r.q0 >= addr2.r.q1 {
		ed.copyx(w, addr2)
		ed.elogdelete(w, addr.r.q0, addr.r.q1)
	} else if addr.r == addr2.r {
		// move to self; no-op
	} else {
		editerror("move overlaps itself")
	}
}

func (ed *edit) m(w *Window, cp *Cmd) bool {
	addr2 := ed.address(cp.mtaddr, ed.mkaddr(w), 0)
	if cp.cmdc == 'm' {
		ed.move(w, addr2)
	} else {
		ed.copyx(w, addr2)
	}
	return true
}

func (ed *edit) s(w *Window, cp *Cmd) bool {
	are := compile(cp.re, "s command")
	text := ed.text(w)
	n := cp.num
	op := -1
	var rp [][]int
	for p1 := ed.addr.r.q0; p1 <= ed.addr.r.q1; {
		m := are.FindForward(text, p1, ed.addr.r.q1, 1)
		if m == nil {
			break
		}
		sel := m[0]
		if sel[0] == sel[1] { // empty match?
			if sel[0] == op {
				p1++
				continue
			}
			p1 = sel[1] + 1
		} else {
			p1 = sel[1]
		}
		op = sel[1]
		n--
		if n > 0 {
			continue
		}
		rp = append(rp, sel)
	}
	rhs := []rune(cp.text)
	didsub := false
	for _, sel := range rp {
		var buf []rune
		for i := 0; i < len(rhs); i++ {
			c := rhs[i]
			if c == '\\' && i < len(rhs)-1 {
				i++
				c = rhs[i]
				if '1' <= c && c <= '9' {
					// A group that took no part in the match is empty.
					if j := 2 * int(c-'0'); j < len(sel) && sel[j] >= 0 {
						buf = append(buf, text[sel[j]:sel[j+1]]...)
					}
				} else {
					buf = append(buf, c)
				}
			} else if c != '&' {
				buf = append(buf, c)
			} else {
				buf = append(buf, text[sel[0]:sel[1]]...)
			}
		}
		ed.elogreplace(w, sel[0], sel[1], buf)
		didsub = true
		if cp.flag == 0 {
			break
		}
	}
	if !didsub && ed.nest == 0 {
		editerror("no substitution")
	}
	ed.setdot(w, ed.addr.r)
	return true
}

func (ed *edit) u(w *Window, cp *Cmd) bool {
	n := cp.num
	undo := true
	if n < 0 {
		n = -n
		undo = false
	}
	b := w.body.GetBuffer()
	for ; n > 0; n-- {
		if undo {
			b.Undo()
		} else {
			b.Redo()
		}
	}
	delete(ed.texts, w)
	return true
}

func (ed *edit) w(w *Window, cp *Cmd) bool {
	if l := ed.logs[w]; l != nil && len(*l) > 0 {
		editerror("can't write file with pending modifications")
	}
	r := ed.cmdname(w, cp.text, false)
	if r == "" {
		editerror("no name specified for 'w' command")
	}
	ed.putfile(w, ed.addr.r.q0, ed.addr.r.q1, r)
	return true
}

// putfile writes w's text from q0 to q1 to the file name. All of it
// written to the file it is from is saved.
func (ed *edit) putfile(w *Window, q0, q1 int, name string) {
	data := []byte(string(ed.text(w)[q0:q1]))
	var err error
	ed.e.await(func() { err = writeFile(name, data) })
	if err != nil {
		ed.warn("can't write file %s: %v\n", name, err)
		return
	}
	if q0 == 0 && q1 == len(ed.text(w)) && name == w.fileName() {
		ed.e.saved(w, w.body.GetBuffer().version)
	}
}

func (ed *edit) runpipe(w *Window, cmd rune, cr string, inserting bool) string {
	r := strings.TrimLeft(cr, " \t")
	if r == "" {
		editerror("no command specified for %c", cmd)
	}
	input := ""
	if inserting {
		ed.setdot(w, ed.addr.r)
		if cmd == '<' || cmd == '|' {
			ed.elogdelete(w, ed.addr.r.q0, ed.addr.r.q1)
		}
		if cmd == '>' || cmd == '|' {
			input = string(ed.text(w)[ed.addr.r.q0:ed.addr.r.q1])
		}
	}
	path, winid := getwd(), 0
	if w != nil {
		path, winid = w.GetFilename(), w.ID
	}
	var out string
	var err error
	ed.e.await(func() { out, err = runCommand(r, path, input, winid) })
	if err != nil {
		ed.warn("%s", out)
		editerror("%v", err)
	}
	switch {
	case !inserting:
		return out
	case cmd == '>':
		ed.warn("%s", out)
	default:
		ed.eloginsert(w, ed.addr.r.q1, []rune(out))
	}
	return ""
}

func (ed *edit) nlcount(w *Window, q0, q1 int) (nl, pnr int) {
	start := q0
	for _, c := range ed.text(w)[q0:q1] {
		if c == '\n' {
			nl++
			start = q0 + 1
		}
		q0++
	}
	return nl, q0 - start
}

const (
	PosnLine = iota
	PosnChars
	PosnLineChars
)

func (ed *edit) printposn(w *Window, mode int) {
	addr := ed.addr
	if name := w.fileName(); name != "" {
		ed.warn("%s:", name)
	}
	switch mode {
	case PosnChars:
		ed.warn("#%d", addr.r.q0)
		if addr.r.q1 != addr.r.q0 {
			ed.warn(",#%d", addr.r.q1)
		}
	case PosnLineChars:
		l1, r1 := ed.nlcount(w, 0, addr.r.q0)
		l1++
		l2, r2 := ed.nlcount(w, addr.r.q0, addr.r.q1)
		l2 += l1
		if l2 == l1 {
			r2 += r1
		}
		ed.warn("%d+#%d", l1, r1)
		if l2 != l1 {
			ed.warn(",%d+#%d", l2, r2)
		}
	default: // PosnLine
		l1, _ := ed.nlcount(w, 0, addr.r.q0)
		l1++
		l2, _ := ed.nlcount(w, addr.r.q0, addr.r.q1)
		l2 += l1
		// check if addr ends with '\n'
		if addr.r.q1 > 0 && addr.r.q1 > addr.r.q0 && ed.text(w)[addr.r.q1-1] == '\n' {
			l2--
		}
		ed.warn("%d", l1)
		if l2 != l1 {
			ed.warn(",%d", l2)
		}
	}
	ed.warn("\n")
}

func (ed *edit) eq(w *Window, cp *Cmd) bool {
	mode := PosnLine
	switch cp.text {
	case "":
	case "#":
		mode = PosnChars
	case "+":
		mode = PosnLineChars
	default:
		editerror("newline expected")
	}
	ed.printposn(w, mode)
	return true
}

func (ed *edit) nl(w *Window, cp *Cmd) bool {
	if cp.addr == nil {
		// First put it on newline boundaries
		a := ed.mkaddr(w)
		ed.addr = ed.lineaddr(0, a, -1)
		a = ed.lineaddr(0, a, 1)
		ed.addr.r.q1 = a.r.q1
		if ed.addr.r == ed.mkaddr(w).r {
			ed.addr = ed.lineaddr(1, ed.mkaddr(w), 1)
		}
	}
	ed.setdot(w, ed.addr.r)
	w.body.Show(ed.addr.r.q0)
	return true
}

func (ed *edit) appendx(w *Window, cp *Cmd, p int) bool {
	ed.eloginsert(w, p, []rune(cp.text))
	ed.setdot(w, Range{p, p})
	return true
}

func (ed *edit) pdisplay(w *Window) bool {
	p2 := min(ed.addr.r.q1, len(ed.text(w)))
	ed.warn("%s", string(ed.text(w)[ed.addr.r.q0:p2]))
	ed.setdot(w, ed.addr.r)
	return true
}

func (ed *edit) pfilename(w *Window) {
	dirtychar := ' '
	if w.IsDirty() {
		dirtychar = '\''
	}
	fc := ' '
	if ed.curtext == w {
		fc = '.'
	}
	ed.warn("%c%c%c %s\n", dirtychar, '+', fc, w.fileName())
}

func (ed *edit) loopcmd(w *Window, cp *Cmd, rp []Range) {
	for _, r := range rp {
		ed.setdot(w, r)
		ed.exec(w, cp)
	}
}

func (ed *edit) looper(w *Window, cp *Cmd, xy bool) {
	var rp []Range
	r := ed.addr.r
	op := r.q0
	if xy {
		op = -1
	}
	ed.nest++
	are := compile(cp.re, fmt.Sprintf("%c command", cp.cmdc))
	var sel []int
	for p := r.q0; p <= r.q1; {
		var tr Range
		if m := are.FindForward(ed.text(w), p, r.q1, 1); m == nil { // no match, but y should still run
			if xy || op > r.q1 {
				break
			}
			tr = Range{op, r.q1}
			p = r.q1 + 1 // exit next loop
		} else {
			sel = m[0]
			if sel[0] == sel[1] { // empty match?
				if sel[0] == op {
					p++
					continue
				}
				p = sel[1] + 1
			} else {
				p = sel[1]
			}
			if xy {
				tr = Range{sel[0], sel[1]}
			} else {
				tr = Range{op, sel[0]}
			}
		}
		if sel != nil {
			op = sel[1]
		}
		rp = append(rp, tr)
	}
	ed.loopcmd(w, cp.cmd, rp)
	ed.nest--
}

func (ed *edit) linelooper(w *Window, cp *Cmd) {
	var rp []Range
	ed.nest++
	r := ed.addr.r
	a3 := Address{Range{r.q0, r.q0}, w}
	a := ed.lineaddr(0, a3, 1)
	linesel := a.r
	for p := r.q0; p < r.q1; p = a3.r.q1 {
		a3.r.q0 = a3.r.q1
		if p != r.q0 || linesel.q1 == p {
			a = ed.lineaddr(1, a3, 1)
			linesel = a.r
		}
		if linesel.q0 >= r.q1 {
			break
		}
		if linesel.q1 >= r.q1 {
			linesel.q1 = r.q1
		}
		if linesel.q1 > linesel.q0 {
			if linesel.q0 >= a3.r.q1 && linesel.q1 > a3.r.q1 {
				a3.r = linesel
				rp = append(rp, linesel)
				continue
			}
		}
		break
	}
	ed.loopcmd(w, cp.cmd, rp)
	ed.nest--
}

// filelooper runs cp's command in each window whose file line cp's
// expression matches, for X, or does not, for Y. A terminal is no file.
func (ed *edit) filelooper(cp *Cmd, XY bool) {
	if ed.glooping != 0 {
		isX := 'Y'
		if XY {
			isX = 'X'
		}
		editerror("can't nest %c command", isX)
	}
	ed.glooping++
	ed.nest++
	var ws []*Window
	for _, w := range ed.e.allWindows() {
		// no auto-execute on files without names
		if w.kind == WinTerm || cp.re == "" && w.fileName() == "" {
			continue
		}
		if cp.re == "" || ed.filematch(w, cp.re) == XY {
			ws = append(ws, w)
		}
	}
	for _, w := range ws {
		ed.exec(w, cp.cmd)
	}
	ed.glooping--
	ed.nest--
}

// compile compiles the regular expression re, used for what.
func compile(re, what string) *regexp.Regexp {
	are, err := regexp.CompileAcme(re)
	if err != nil {
		editerror("bad regexp in %s", what)
	}
	return are
}

// nextmatch returns the match of re in w's text nearest p in direction
// sign, wrapping around the ends of the text, as acme's does.
func (ed *edit) nextmatch(w *Window, re string, p int, sign int) Range {
	are := compile(re, "command address")
	text := ed.text(w)
	find := func(p int) []int {
		if m := are.FindForward(text, p, -1, 1); m != nil {
			return m[0]
		}
		if m := are.FindForward(text, 0, -1, 1); m != nil {
			return m[0]
		}
		return nil
	}
	if sign < 0 {
		find = func(p int) []int {
			if m := are.FindBackward(text, 0, p, 1); m != nil {
				return m[0]
			}
			if m := are.FindBackward(text, 0, len(text), 1); m != nil {
				return m[0]
			}
			return nil
		}
	}
	m := find(p)
	if m == nil {
		editerror("no match for regexp")
	}
	// An empty match at p is no move; look past it.
	if sign >= 0 && m[0] == m[1] && m[0] == p {
		if p++; p > len(text) {
			p = 0
		}
		m = find(p)
	} else if sign < 0 && m[0] == m[1] && m[1] == p {
		if p--; p < 0 {
			p = len(text)
		}
		m = find(p)
	}
	if m == nil {
		editerror("address")
	}
	return Range{m[0], m[1]}
}

func (ed *edit) address(ap *Addr, a Address, sign int) Address {
	w := a.w
	for {
		switch ap.typ {
		case 'l':
			a = ed.lineaddr(ap.num, a, sign)
		case '#':
			a = ed.charaddr(ap.num, a, sign)
		case '.':
			a = ed.mkaddr(w)
		case '$':
			n := len(ed.text(w))
			a.r = Range{n, n}
		case '\'':
			editerror("can't handle '")
		case '?':
			sign = -sign
			if sign == 0 {
				sign = -1
			}
			fallthrough
		case '/':
			q := a.r.q1
			if sign < 0 {
				q = a.r.q0
			}
			a.r = ed.nextmatch(w, ap.re, q, sign)
		case '"':
			w = ed.matchfile(ap.re)
			a = ed.mkaddr(w)
		case '*':
			a.r = Range{0, len(ed.text(w))}
			return a
		case ',', ';':
			var a1, a2 Address
			if ap.left != nil {
				a1 = ed.address(ap.left, a, 0)
			} else {
				a1 = Address{Range{0, 0}, a.w}
			}
			if ap.typ == ';' {
				w = a1.w
				a = a1
				ed.setdot(w, a1.r)
			}
			if ap.next != nil {
				a2 = ed.address(ap.next, a, 0)
			} else {
				a2 = Address{Range{0, len(ed.text(w))}, a.w}
			}
			if a1.w != a2.w {
				editerror("addresses in different files")
			}
			a = Address{Range{a1.r.q0, a2.r.q1}, a1.w}
			if a.r.q1 < a.r.q0 {
				editerror("addresses out of order")
			}
			return a
		case '+', '-':
			sign = 1
			if ap.typ == '-' {
				sign = -1
			}
			if ap.next == nil || ap.next.typ == '+' || ap.next.typ == '-' {
				a = ed.lineaddr(1, a, sign)
			}
		}
		if ap = ap.next; ap == nil {
			return a
		}
	}
}

// isfile reports whether w holds a file, as b and " name them: not a
// directory, +Errors or a terminal.
func isfile(w *Window) bool { return w.kind == WinFile }

// toWindow returns the window holding the file named r.
func (ed *edit) toWindow(r string) *Window {
	r = strings.TrimLeft(r, " \t\n")
	for _, w := range ed.e.allWindows() {
		if isfile(w) && w.fileName() == r {
			return w
		}
	}
	editerror("no such file\"%v\"", r)
	return nil
}

// matchfile returns the one window whose file line r matches.
func (ed *edit) matchfile(r string) *Window {
	var match *Window
	for _, w := range ed.e.allWindows() {
		if isfile(w) && ed.filematch(w, r) {
			if match != nil {
				editerror("too many files match \"%v\"", r)
			}
			match = w
		}
	}
	if match == nil {
		editerror("no file matches \"%v\"", r)
	}
	return match
}

// filematch reports whether r matches w's line in the file list, as b
// prints it.
func (ed *edit) filematch(w *Window, r string) bool {
	are := compile(r, "file match")
	dmark := ' '
	if w.IsDirty() {
		dmark = '\''
	}
	fmark := ' '
	if ed.curtext == w {
		fmark = '.'
	}
	buf := []rune(fmt.Sprintf("%c%c%c %s\n", dmark, '+', fmark, w.fileName()))
	return are.FindForward(buf, 0, len(buf), 1) != nil
}

func (ed *edit) charaddr(l int, addr Address, sign int) Address {
	if sign == 0 {
		addr.r.q0 = l
		addr.r.q1 = l
	} else if sign < 0 {
		addr.r.q0 -= l
		addr.r.q1 = addr.r.q0
	} else if sign > 0 {
		addr.r.q1 += l
		addr.r.q0 = addr.r.q1
	}
	if addr.r.q0 < 0 || addr.r.q1 > len(ed.text(addr.w)) {
		editerror("address out of range")
	}
	return addr
}

func (ed *edit) lineaddr(l int, addr Address, sign int) Address {
	t := ed.text(addr.w)
	a := Address{w: addr.w}
	var n, p int
	if sign >= 0 {
		if l == 0 {
			if sign == 0 || addr.r.q1 == 0 {
				return a
			}
			a.r.q0 = addr.r.q1
			p = addr.r.q1 - 1
		} else {
			if sign == 0 || addr.r.q1 == 0 {
				p = 0
				n = 1
			} else {
				p = addr.r.q1 - 1
				if t[p] == '\n' {
					n = 1
				}
				p++
			}
			for n < l {
				if p >= len(t) {
					editerror("address out of range")
				}
				if t[p] == '\n' {
					n++
				}
				p++
			}
			a.r.q0 = p
		}
		for p < len(t) {
			p++
			if t[p-1] == '\n' {
				break
			}
		}
		a.r.q1 = p
	} else {
		p = addr.r.q0
		if l == 0 {
			a.r.q1 = addr.r.q0
		} else {
			for n = 0; n < l; { // always runs once
				if p == 0 {
					if n++; n != l {
						editerror("address out of range")
					}
				} else if t[p-1] != '\n' {
					p--
				} else if n++; n != l {
					p--
				}
			}
			a.r.q1 = p
			if p > 0 {
				p--
			}
		}
		for p > 0 && t[p-1] != '\n' { // lines start after a newline
			p--
		}
		a.r.q0 = p
	}
	return a
}

// cmdname returns the file name str gives, in w's directory, or w's own
// for none. Set, or for a window with no name, it names w, whose text is
// then not that file's.
func (ed *edit) cmdname(w *Window, str string, set bool) string {
	if str == "" {
		// no name; use existing
		return w.fileName()
	}
	r := ""
	if s := strings.TrimLeft(str, " \t"); s != "" {
		r = s
		if !filepath.IsAbs(s) {
			r = filepath.Join(w.GetDir(), s)
		}
		for _, o := range ed.e.allWindows() {
			if o != w && o.fileName() == r {
				ed.warn("warning: duplicate file name \"%s\"\n", r)
			}
		}
		if w.fileName() == "" {
			set = true
		}
	}
	if set && r != w.fileName() {
		w.SetName(r)
		w.markSaved(-1)
	}
	return r
}

// ---- elog ----

// An elog is a log of changes made by editing commands. Three reasons for
// this:
// 1) We want addresses in commands to apply to old file, not file-in-change.
// 2) It's difficult to track changes correctly as things move, e.g. ,x m$
// 3) This gives an opportunity to optimize by merging adjacent changes.
//
// The changes are expected in order of offset and are applied last first.
// One out of order is warned about, once in an Edit, and applied anyway.
type elog []elogop

type elogop struct {
	typ byte // 'd'elete, 'i'nsert or 'r'eplace
	q0  int  // location of change
	nd  int  // number of runes to delete
	r   []rune
}

const wsequence = "warning: changes out of sequence\n"

// elog returns w's edit log, for a change. A terminal's text is the
// program's, not to change.
func (ed *edit) elog(w *Window) *elog {
	if w.kind == WinTerm {
		editerror("can't change terminal %s", w.GetFilename())
	}
	l := ed.logs[w]
	if l == nil {
		l = new(elog)
		ed.logs[w] = l
	}
	return l
}

// last returns the change logged last, if any, and warns if q, where the
// next one is, comes before end.
func (ed *edit) last(l *elog, q int, end func(*elogop) int) *elogop {
	if len(*l) == 0 {
		return nil
	}
	eo := &(*l)[len(*l)-1]
	if q < end(eo) {
		if !ed.warned {
			ed.warn(wsequence)
		}
		ed.warned = true
		return nil
	}
	return eo
}

func start(eo *elogop) int { return eo.q0 }

func (ed *edit) elogreplace(w *Window, q0, q1 int, r []rune) {
	if q0 == q1 && len(r) == 0 {
		return
	}
	l := ed.elog(w)
	ed.last(l, q0, start)
	*l = append(*l, elogop{'r', q0, q1 - q0, slices.Clone(r)})
}

func (ed *edit) eloginsert(w *Window, q0 int, r []rune) {
	if len(r) == 0 {
		return
	}
	l := ed.elog(w)
	// try to merge with previous
	if eo := ed.last(l, q0, start); eo != nil && eo.typ == 'i' && eo.q0 == q0 {
		eo.r = append(eo.r, r...)
		return
	}
	*l = append(*l, elogop{'i', q0, 0, slices.Clone(r)})
}

func (ed *edit) elogdelete(w *Window, q0, q1 int) {
	if q0 == q1 {
		return
	}
	l := ed.elog(w)
	// try to merge with previous
	if eo := ed.last(l, q0, func(eo *elogop) int { return eo.q0 + eo.nd }); eo != nil && eo.typ == 'd' && eo.q0+eo.nd == q0 {
		eo.nd += q1 - q0
		return
	}
	*l = append(*l, elogop{'d', q0, q1 - q0, nil})
}

// elogapply makes w's logged changes to its text, last first, as one to
// undo. Dot moves with the text around it, as acme's textinsert and
// textdelete move it, and an empty dot where text is inserted selects it.
func (ed *edit) elogapply(w *Window, l elog) {
	b := w.body.GetBuffer()
	q0, q1 := b.q0, b.q1
	b.saveState()
	for i := len(l) - 1; i >= 0; i-- {
		eo := l[i]
		n := b.Len()
		p0, p1 := min(eo.q0, n), min(eo.q0+eo.nd, n)
		b.replace(p0, p1, string(eo.r))
		if d := p1 - p0; d > 0 {
			if p0 < q0 {
				q0 -= min(d, q0-p0)
			}
			if p0 < q1 {
				q1 -= min(d, q1-p0)
			}
		}
		if m := len(eo.r); m > 0 {
			if p0 < q1 {
				q1 += m
			}
			if p0 < q0 {
				q0 += m
			}
			if q0 == eo.q0 && q1 == eo.q0 {
				q1 += m
			}
		}
	}
	// Changes out of order make bad addresses; keep dot in the text.
	if n := b.Len(); q0 > n || q1 > n || q0 > q1 {
		if !ed.warned {
			ed.warn("elogapply: can't happen %d %d %d\n", q0, q1, n)
		}
		q1 = min(q1, n)
		q0 = min(q0, q1)
	}
	b.q0, b.q1 = q0, q1
}
