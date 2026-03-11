// Package sregx implements a structural regular expression engine for the Peak editor.
// It is inspired by the Acme text editor's 'Edit' command and provides a simplified
// implementation of its recursive descent parser and command execution logic.
// It utilizes a modified version of Edwood's regexp engine for Plan 9 semantics.

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aleksana/peak/regexp"
)

type Range struct {
	q0, q1 int
}

type Addr struct {
	typ  rune // # (byte addr), l (line addr), / ? . $ + - , ; "
	re   string
	left *Addr // left side of , and ;
	num  int
	next *Addr // or right side of , and ;
}

type Cmd struct {
	addr   *Addr  // address (range of text)
	re     string // regular expression for e.g. 'x'
	cmd    *Cmd   // target of x, g, {, etc.
	text   string // text of a, c, i; rhs of s
	mtaddr *Addr  // address for m, t
	next   *Cmd   // pointer to next element in braces
	num    int
	flag   rune // 'g' for substitution
	cmdc   rune // command character; 'x', 's', etc.
}

type SregxResult struct {
	Cmd *Cmd
}

type Context struct {
	Editor *Editor
	Buffer *Buffer
	Out    io.Writer
}

var lastpat string

func SregxCompile(s string, out io.Writer) (*SregxResult, error) {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	cp := &cmdParser{buf: []rune(s)}
	cmd, err := cp.parse(0)
	if err != nil {
		return nil, err
	}
	return &SregxResult{Cmd: cmd}, nil
}

type cmdParser struct {
	buf []rune
	pos int
}

func (cp *cmdParser) getch() rune {
	if cp.pos >= len(cp.buf) {
		return -1
	}
	c := cp.buf[cp.pos]
	cp.pos++
	return c
}

func (cp *cmdParser) ungetch() {
	if cp.pos > 0 {
		cp.pos--
	}
}

func (cp *cmdParser) nextc() rune {
	if cp.pos >= len(cp.buf) {
		return -1
	}
	return cp.buf[cp.pos]
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

func (cp *cmdParser) getnum(signok bool) int {
	n := 0
	sign := 1
	if signok && cp.nextc() == '-' {
		sign = -1
		cp.getch()
	}
	c := cp.nextc()
	if c < '0' || '9' < c {
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

func (cp *cmdParser) getregexp(delim rune) (string, error) {
	var buf strings.Builder
	for {
		c := cp.getch()
		if c == '\\' {
			if cp.nextc() == delim {
				c = cp.getch()
			} else if cp.nextc() == '\\' {
				buf.WriteRune('\\')
				c = cp.getch()
			}
		} else if c == delim || c == '\n' || c == -1 {
			break
		}
		buf.WriteRune(c)
	}
	if len(buf.String()) > 0 {
		lastpat = buf.String()
	}
	if len(lastpat) == 0 {
		return "", fmt.Errorf("no regular expression")
	}
	return lastpat, nil
}

func (cp *cmdParser) getrhs(delim rune) (string, error) {
	var buf strings.Builder
	for {
		c := cp.getch()
		if c <= 0 || c == delim || c == '\n' {
			break
		}
		if c == '\\' {
			c = cp.getch()
			if c <= 0 {
				return "", fmt.Errorf("bad right hand side")
			}
			if c == '\n' {
				cp.ungetch()
				c = '\\'
			} else if c == 'n' {
				c = '\n'
			} else if c != delim {
				buf.WriteRune('\\')
			}
		}
		buf.WriteRune(c)
	}
	cp.ungetch()
	return buf.String(), nil
}

func (cp *cmdParser) simpleaddr() (*Addr, error) {
	addr := &Addr{}
	switch cp.skipbl() {
	case '#':
		addr.typ = cp.getch()
		addr.num = cp.getnum(false)
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		addr.typ = 'l'
		addr.num = cp.getnum(false)
	case '/', '?', '"':
		addr.typ = cp.getch()
		re, err := cp.getregexp(addr.typ)
		if err != nil {
			return nil, err
		}
		addr.re = re
	case '.', '$', '+', '-':
		addr.typ = cp.getch()
	default:
		return nil, nil
	}
	next, err := cp.simpleaddr()
	if err != nil {
		return nil, err
	}
	if next != nil {
		if next.typ == '.' || next.typ == '$' {
			if addr.typ != '"' {
				return nil, fmt.Errorf("bad address syntax")
			}
		} else if next.typ == 'l' || next.typ == '#' || next.typ == '/' || next.typ == '?' {
			if addr.typ != '+' && addr.typ != '-' {
				nap := &Addr{typ: '+', next: next}
				addr.next = nap
				return addr, nil
			}
		}
		addr.next = next
	}
	return addr, nil
}

func (cp *cmdParser) compoundaddr() (*Addr, error) {
	left, err := cp.simpleaddr()
	if err != nil {
		return nil, err
	}
	typ := cp.skipbl()
	if typ != ',' && typ != ';' {
		return left, nil
	}
	cp.getch()
	right, err := cp.compoundaddr()
	if err != nil {
		return nil, err
	}
	return &Addr{typ: typ, left: left, next: right}, nil
}

func (cp *cmdParser) parse(nest int) (*Cmd, error) {
	addr, err := cp.compoundaddr()
	if err != nil {
		return nil, err
	}
	cp.skipbl()
	c := cp.getch()
	if c == -1 || c == '\n' {
		return &Cmd{addr: addr, cmdc: '\n'}, nil
	}
	cmd := &Cmd{addr: addr, cmdc: c}
	i := cmdlookup(c)
	if i >= 0 {
		ct := &cmdtab[i]
		if ct.count != 0 {
			cmd.num = cp.getnum(ct.count == 2)
		}
		if ct.regexp {
			cp.skipbl()
			delim := cp.getch()
			if delim == '\n' || delim < 0 {
				return nil, fmt.Errorf("address missing")
			}
			re, err := cp.getregexp(delim)
			if err != nil {
				return nil, err
			}
			cmd.re = re
			if ct.cmdc == 's' {
				cmd.text, err = cp.getrhs(delim)
				if err != nil {
					return nil, err
				}
				if cp.nextc() == delim {
					cp.getch()
					if cp.nextc() == 'g' {
						cmd.flag = cp.getch()
					}
				}
			}
		}
		if ct.addr {
			var err error
			cmd.mtaddr, err = cp.simpleaddr()
			if err != nil {
				return nil, err
			}
			if cmd.mtaddr == nil {
				return nil, fmt.Errorf("bad address")
			}
		}
		if ct.text {
			cmd.text, err = cp.collecttext()
			if err != nil {
				return nil, err
			}
		}
		if ct.defcmd != 0 {
			if cp.skipbl() == '\n' {
				cp.getch()
				cmd.cmd = &Cmd{cmdc: ct.defcmd}
			} else {
				cmd.cmd, err = cp.parse(nest)
				if err != nil {
					return nil, err
				}
			}
		}
	} else if c == '{' {
		var head, last *Cmd
		for {
			if head != nil && cp.skipbl() == '\n' {
				cp.getch()
			}
			if cp.nextc() == '}' {
				cp.getch()
				break
			}
			nc, err := cp.parse(nest + 1)
			if err != nil {
				return nil, err
			}
			if nc == nil {
				break
			}
			if head == nil {
				head = nc
			} else {
				last.next = nc
			}
			last = nc
		}
		cmd.cmd = head
	} else if c == '}' {
		if nest == 0 {
			return nil, fmt.Errorf("right brace with no left brace")
		}
		return nil, nil
	} else if c == '|' || c == '>' || c == '<' {
		cmd.text = cp.collecttoken("\n")
	} else {
		return nil, fmt.Errorf("unknown command %c", c)
	}
	return cmd, nil
}

func (cp *cmdParser) collecttoken(end string) string {
	var s strings.Builder
	for {
		c := cp.getch()
		if c <= 0 || strings.ContainsRune(end, c) {
			break
		}
		s.WriteRune(c)
	}
	return s.String()
}

func (cp *cmdParser) collecttext() (string, error) {
	if cp.skipbl() == '\n' {
		cp.getch()
		var buf strings.Builder
		for {
			var line strings.Builder
			for {
				c := cp.getch()
				if c <= 0 || c == '\n' {
					break
				}
				line.WriteRune(c)
			}
			if line.String() == "." {
				break
			}
			buf.WriteString(line.String())
			buf.WriteRune('\n')
		}
		return buf.String(), nil
	}
	delim := cp.getch()
	s, err := cp.getrhs(delim)
	if err != nil {
		return "", err
	}
	if cp.nextc() == delim {
		cp.getch()
	}
	return s, nil
}

type cmdtab_entry struct {
	cmdc    rune
	text    bool
	regexp  bool
	addr    bool // for m, t
	defcmd  rune
	defaddr int // 0: no, 1: dot, 2: all
	count   int // 0: no, 1: unsigned, 2: signed
}

var cmdtab = []cmdtab_entry{
	{'\n', false, false, false, 0, 1, 0},
	{'a', true, false, false, 0, 1, 0},
	{'c', true, false, false, 0, 1, 0},
	{'d', false, false, false, 0, 1, 0},
	{'g', false, true, false, 'p', 1, 0},
	{'i', true, false, false, 0, 1, 0},
	{'m', false, false, true, 0, 1, 0},
	{'p', false, false, false, 0, 1, 0},
	{'s', false, true, false, 0, 1, 1},
	{'t', false, false, true, 0, 1, 0},
	{'u', false, false, false, 0, 0, 2},
	{'v', false, true, false, 'p', 1, 0},
	{'w', false, false, false, 0, 2, 0},
	{'x', false, true, false, 'p', 1, 0},
	{'y', false, true, false, 'p', 1, 0},
	{'=', false, false, false, 0, 1, 0},
	{'X', false, true, false, 'f', 0, 0},
	{'Y', false, true, false, 'f', 0, 0},
}

func cmdlookup(c rune) int {
	for i, ent := range cmdtab {
		if ent.cmdc == c {
			return i
		}
	}
	return -1
}

func compileRegex(pat string) (*regexp.Regexp, error) {
	return regexp.CompileAcme(pat)
}

func (cmd *Cmd) Execute(ctx *Context, dot Range) (Range, bool) {
	addr := dot
	runes := ctx.Buffer.GetRunes()
	if cmd.addr != nil {
		addr = cmdaddress(cmd.addr, dot, runes, 0)
	} else {
		i := cmdlookup(cmd.cmdc)
		if i >= 0 && cmdtab[i].defaddr == 2 {
			addr = Range{0, len(runes)}
		}
	}

	switch cmd.cmdc {
	case '\n':
		return addr, true
	case 'p':
		if ctx.Out != nil {
			ctx.Out.Write([]byte(string(runes[addr.q0:addr.q1])))
			ctx.Out.Write([]byte{'\n'})
		}
		return addr, true
	case 'd':
		ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q1, nil)
		return Range{addr.q0, addr.q0}, true
	case 'a':
		ctx.Buffer.ReplaceRangeRunes(addr.q1, addr.q1, []rune(cmd.text))
		return Range{addr.q1, addr.q1 + len([]rune(cmd.text))}, true
	case 'i':
		ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q0, []rune(cmd.text))
		return Range{addr.q0, addr.q0 + len([]rune(cmd.text))}, true
	case 'c':
		ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q1, []rune(cmd.text))
		return Range{addr.q0, addr.q0 + len([]rune(cmd.text))}, true
	case 'm', 't':
		addr2 := cmdaddress(cmd.mtaddr, dot, runes, 0)
		text := append([]rune{}, runes[addr.q0:addr.q1]...)
		if cmd.cmdc == 'm' {
			if addr.q1 <= addr2.q0 {
				ctx.Buffer.ReplaceRangeRunes(addr2.q1, addr2.q1, text)
				ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q1, nil)
			} else if addr.q0 >= addr2.q1 {
				ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q1, nil)
				ctx.Buffer.ReplaceRangeRunes(addr2.q1, addr2.q1, text)
			} else {
				// overlap, ignore as in Acme
			}
		} else {
			ctx.Buffer.ReplaceRangeRunes(addr2.q1, addr2.q1, text)
		}
		return addr, true
	case 'x', 'y':
		re, err := compileRegex(cmd.re)
		if err != nil {
			return addr, false
		}
		text := runes[addr.q0:addr.q1]

		var rp []Range
		if cmd.cmdc == 'x' {
			matches := re.FindForward(text, 0, -1, -1)
			for _, m := range matches {
				rp = append(rp, Range{addr.q0 + m[0], addr.q0 + m[1]})
			}
		} else {
			matches := re.FindForward(text, 0, -1, -1)
			op := 0
			for _, m := range matches {
				rp = append(rp, Range{addr.q0 + op, addr.q0 + m[0]})
				op = m[1]
			}
			rp = append(rp, Range{addr.q0 + op, addr.q1})
		}

		for i := len(rp) - 1; i >= 0; i-- {
			cmd.cmd.Execute(ctx, rp[i])
		}
		return addr, true
	case 's':
		re, err := compileRegex(cmd.re)
		if err != nil {
			return addr, false
		}
		text := runes[addr.q0:addr.q1]
		
		var matches [][]int
		all := re.FindForward(text, 0, -1, -1)
		if len(all) >= cmd.num {
			if cmd.flag == 'g' {
				matches = all[cmd.num-1:]
			} else {
				matches = [][]int{all[cmd.num-1]}
			}
		}

		if len(matches) == 0 {
			return addr, true
		}

		// Apply replacements from back to front to avoid offset issues
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			expanded := expand(cmd.text, text, m)
			q0 := addr.q0 + m[0]
			q1 := addr.q0 + m[1]
			ctx.Buffer.ReplaceRangeRunes(q0, q1, []rune(expanded))
		}
		return addr, true
	case 'g', 'v':
		re, err := compileRegex(cmd.re)
		if err != nil {
			return addr, false
		}
		match := re.MatchString(string(runes[addr.q0:addr.q1]))
		if (cmd.cmdc == 'g' && match) || (cmd.cmdc == 'v' && !match) {
			return cmd.cmd.Execute(ctx, addr)
		}
		return addr, true
	case 'X', 'Y':
		re, err := compileRegex(cmd.re)
		if err != nil {
			return addr, false
		}
		for _, col := range ctx.Editor.columns {
			for _, win := range col.windows {
				filename := win.GetFilename()
				match := re.MatchString(filename)
				if (cmd.cmdc == 'X' && match) || (cmd.cmdc == 'Y' && !match) {
					subCtx := &Context{Editor: ctx.Editor, Buffer: win.body.buffer, Out: ctx.Out}
					subDot := Range{win.body.buffer.CursorToRuneOffset(win.body.buffer.cursor), win.body.buffer.CursorToRuneOffset(win.body.buffer.cursor)}
					if win.body.buffer.selectionStart != nil {
						s, e := win.body.buffer.orderedSelection()
						subDot = Range{win.body.buffer.CursorToRuneOffset(s), win.body.buffer.CursorToRuneOffset(e)}
					}
					cmd.cmd.Execute(subCtx, subDot)
				}
			}
		}
		return addr, true
	case 'u':
		for i := 0; i < cmd.num; i++ {
			ctx.Buffer.Undo()
		}
		return addr, true
	case 'w':
		filename := cmd.text
		if filename == "" {
			filename = ctx.Editor.active.GetFilename()
		}
		if filename != "" && !strings.HasSuffix(filename, "/") {
			err := os.WriteFile(filename, []byte(ctx.Buffer.GetText()), 0644)
			if err != nil && ctx.Out != nil {
				ctx.Out.Write([]byte(err.Error() + "\n"))
			}
		}
		return addr, true
	case '=':
		if ctx.Out != nil {
			ctx.Out.Write([]byte(fmt.Sprintf("#%d,#%d\n", addr.q0, addr.q1)))
		}
		return addr, true
	case '{':
		curr := cmd.cmd
		for curr != nil {
			addr, _ = curr.Execute(ctx, addr)
			curr = curr.next
		}
		return addr, true
	case '|', '>', '<':
		input := string(runes[addr.q0:addr.q1])
		out, err := runPipe(cmd.cmdc, cmd.text, input)
		if err != nil {
			if ctx.Out != nil {
				ctx.Out.Write([]byte(err.Error() + "\n"))
			}
			return addr, false
		}
		if cmd.cmdc == '|' || cmd.cmdc == '<' {
			ctx.Buffer.ReplaceRangeRunes(addr.q0, addr.q1, []rune(out))
			return Range{addr.q0, addr.q0 + len([]rune(out))}, true
		}
		if ctx.Out != nil && len(out) > 0 {
			ctx.Out.Write([]byte(out))
		}
		return addr, true
	}
	return addr, true
}

func expand(repl string, text []rune, match []int) string {
	var buf strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '&' {
			buf.WriteString(string(text[match[0]:match[1]]))
		} else if c == '\\' && i+1 < len(repl) {
			i++
			nc := repl[i]
			if nc >= '1' && nc <= '9' {
				n := int(nc - '0')
				if n*2+1 < len(match) && match[n*2] >= 0 {
					buf.WriteString(string(text[match[n*2]:match[n*2+1]]))
				}
			} else {
				buf.WriteByte(nc)
			}
		} else {
			buf.WriteByte(c)
		}
	}
	return buf.String()
}

func runPipe(cmd rune, shellCmd, input string) (string, error) {
	c := exec.Command("sh", "-c", shellCmd)
	if cmd == '|' || cmd == '>' {
		c.Stdin = strings.NewReader(input)
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

func cmdaddress(ap *Addr, a Range, runes []rune, sign int) Range {
	for {
		switch ap.typ {
		case 'l':
			a = lineaddr(ap.num, a, runes, sign)
			sign = 0
		case '#':
			a = charaddr(ap.num, a, runes, sign)
			sign = 0
		case '.':
			sign = 0
		case '$':
			size := len(runes)
			a = Range{size, size}
			sign = 0
		case '/':
			a = nextmatch(runes, ap.re, a, sign)
			sign = 0
		case '?':
			a = nextmatch(runes, ap.re, a, -1)
			sign = 0
		case '"':
			sign = 0
		case ',':
			var a1, a2 Range
			if ap.left != nil {
				a1 = cmdaddress(ap.left, a, runes, 0)
			} else {
				a1 = Range{0, 0}
			}
			if ap.next != nil {
				a2 = cmdaddress(ap.next, a, runes, 0)
			} else {
				size := len(runes)
				a2 = Range{size, size}
			}
			return Range{a1.q0, a2.q1}
		case ';':
			var a1, a2 Range
			if ap.left != nil {
				a1 = cmdaddress(ap.left, a, runes, 0)
			} else {
				a1 = Range{0, 0}
			}
			if ap.next != nil {
				a2 = cmdaddress(ap.next, a1, runes, 0)
			} else {
				size := len(runes)
				a2 = Range{size, size}
			}
			return Range{a1.q0, a2.q1}
		case '+':
			sign = 1
			if ap.next == nil || (ap.next.typ != 'l' && ap.next.typ != '#' && ap.next.typ != '/' && ap.next.typ != '?') {
				a = lineaddr(1, a, runes, sign)
				sign = 0
			}
		case '-':
			sign = -1
			if ap.next == nil || (ap.next.typ != 'l' && ap.next.typ != '#' && ap.next.typ != '/' && ap.next.typ != '?') {
				a = lineaddr(1, a, runes, sign)
				sign = 0
			}
		}
		ap = ap.next
		if ap == nil {
			break
		}
	}
	return a
}

func lineaddr(l int, addr Range, runes []rune, sign int) Range {
	n := 0
	p := 0
	if sign >= 0 {
		if l == 0 {
			if sign == 0 || addr.q1 == 0 {
				return Range{0, 0}
			}
			p = addr.q1
		} else {
			if sign == 0 || addr.q1 == 0 {
				p = 0
				n = 1
			} else {
				p = addr.q1 - 1
				if p >= 0 && p < len(runes) && runes[p] == '\n' {
					n = 1
				}
				p++
			}
			for n < l {
				if p >= len(runes) {
					return Range{len(runes), len(runes)}
				}
				if runes[p] == '\n' {
					n++
				}
				p++
			}
		}
		q0 := p
		for p < len(runes) && runes[p] != '\n' {
			p++
		}
		return Range{q0, p}
	} else {
		p = addr.q0
		if l == 0 {
			return Range{addr.q0, addr.q0}
		}
		for n = 0; n < l; {
			if p == 0 {
				n++
				if n != l {
					return Range{0, 0}
				}
			} else {
				c := runes[p-1]
				n++
				if c != '\n' || n != l {
					p--
				}
			}
		}
		q1 := p
		if p > 0 {
			p--
		}
		for p > 0 && runes[p-1] != '\n' {
			p--
		}
		return Range{p, q1}
	}
}

func charaddr(l int, addr Range, runes []rune, sign int) Range {
	size := len(runes)
	if sign == 0 {
		addr.q0 = l
		addr.q1 = l
	} else if sign < 0 {
		addr.q0 -= l
		addr.q1 = addr.q0
	} else {
		addr.q1 += l
		addr.q0 = addr.q1
	}
	if addr.q0 < 0 {
		addr.q0 = 0
	}
	if addr.q1 > size {
		addr.q1 = size
	}
	return addr
}

func nextmatch(runes []rune, pat string, addr Range, sign int) Range {
	re, err := compileRegex(pat)
	if err != nil {
		return addr
	}

	if sign >= 0 {
		matches := re.FindForward(runes, addr.q1, -1, 1)
		if len(matches) > 0 {
			return Range{matches[0][0], matches[0][1]}
		}
		// Wrap around
		matches = re.FindForward(runes, 0, addr.q1, 1)
		if len(matches) > 0 {
			return Range{matches[0][0], matches[0][1]}
		}
	} else {
		matches := re.FindBackward(runes, 0, addr.q0, 1)
		if len(matches) > 0 {
			return Range{matches[0][0], matches[0][1]}
		}
		// Wrap around
		matches = re.FindBackward(runes, addr.q0, len(runes), 1)
		if len(matches) > 0 {
			return Range{matches[0][0], matches[0][1]}
		}
	}
	return addr
}
