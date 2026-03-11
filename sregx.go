/*
MIT License

Copyright (c) 2021: Zachary Yedidia.

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

package main

import (
	"bytes"
	"io"
	"regexp"
	"strconv"

	"github.com/zyedidia/gpeg/capture"
	"github.com/zyedidia/gpeg/charset"
	"github.com/zyedidia/gpeg/input"
	"github.com/zyedidia/gpeg/memo"
	p "github.com/zyedidia/gpeg/pattern"
	"github.com/zyedidia/gpeg/vm"
)

// A Command modifies an input byte slice in some way and returns the new one.
type Command interface {
	Evaluate(b []byte) []byte
}

// A CommandPipeline represents a list of commands chained together in a
// pipeline.
type CommandPipeline []Command

// Evaluate runs each command in the pipeline, passing the previous command's
// output as the next command's input.
func (cp CommandPipeline) Evaluate(b []byte) []byte {
	for _, c := range cp {
		b = c.Evaluate(b)
	}
	return b
}

type X struct {
	Patt *regexp.Regexp
	Cmd  Command
}

func (x X) Evaluate(b []byte) []byte {
	return x.Patt.ReplaceAllFunc(b, func(b []byte) []byte {
		return x.Cmd.Evaluate(b)
	})
}

type Y struct {
	Patt *regexp.Regexp
	Cmd  Command
}

func (y Y) Evaluate(b []byte) []byte {
	return ReplaceAllComplementFunc(y.Patt, b, func(b []byte) []byte {
		return y.Cmd.Evaluate(b)
	})
}

type G struct {
	Patt *regexp.Regexp
	Cmd  Command
}

func (g G) Evaluate(b []byte) []byte {
	if g.Patt.Match(b) {
		return g.Cmd.Evaluate(b)
	}
	return b
}

type V struct {
	Patt *regexp.Regexp
	Cmd  Command
}

func (v V) Evaluate(b []byte) []byte {
	if !v.Patt.Match(b) {
		return v.Cmd.Evaluate(b)
	}
	return b
}

type S struct {
	Patt    *regexp.Regexp
	Replace []byte
}

func (s S) Evaluate(b []byte) []byte {
	return s.Patt.ReplaceAll(b, s.Replace)
}

type P struct {
	W io.Writer
}

func (p P) Evaluate(b []byte) []byte {
	p.W.Write(b)
	return b
}

type D struct{}

func (d D) Evaluate(b []byte) []byte {
	return []byte{}
}

type C struct {
	Change []byte
}

func (c C) Evaluate(b []byte) []byte {
	return c.Change
}

type None struct{}

func (n None) Evaluate(b []byte) []byte {
	return b
}

type N struct {
	Start int
	End   int
	Cmd   Command
}

func (n N) Evaluate(b []byte) []byte {
	if n.Start < 0 {
		n.Start = len(b) + 1 + n.Start
	}
	if n.End < 0 {
		n.End = len(b) + 1 + n.End
	}
	n.Start = clamp(n.Start, 0, len(b))
	n.End = clamp(n.End, 0, len(b))
	return ReplaceSlice(b, n.Start, n.End, n.Cmd.Evaluate(b[n.Start:n.End]))
}

type L struct {
	Start int
	End   int
	Cmd   Command
}

func (l L) Evaluate(b []byte) []byte {
	if l.Start < 0 || l.End < 0 {
		nlines := bytes.Count(b, []byte{'\n'})
		if l.Start < 0 {
			l.Start = nlines + 1 + l.Start
		}
		if l.End < 0 {
			l.End = nlines + 1 + l.End
		}
	}
	start := IndexN(b, []byte{'\n'}, l.Start) + 1
	end := IndexN(b, []byte{'\n'}, l.End) + 1
	start = clamp(start, 0, len(b))
	end = clamp(end, 0, len(b))
	return ReplaceSlice(b, start, end, l.Cmd.Evaluate(b[start:end]))
}

type U struct {
	Evaluator Evaluator
}

func (u U) Evaluate(b []byte) []byte {
	return u.Evaluator(b)
}

type Evaluator func(b []byte) []byte

func ReplaceAllComplementFunc(re *regexp.Regexp, b []byte, repl func([]byte) []byte) []byte {
	matches := re.FindAllIndex(b, -1)
	buf := make([]byte, 0, len(b))
	beg := 0
	end := 0
	for _, match := range matches {
		end = match[0]
		if match[1] != 0 {
			buf = append(buf, repl(b[beg:end])...)
			buf = append(buf, b[end:match[1]]...)
		}
		beg = match[1]
	}
	if end != len(b) {
		buf = append(buf, repl(b[beg:])...)
	}
	return buf
}

func IndexN(b, sep []byte, n int) (index int) {
	index, idx, sepLen := 0, -1, len(sep)
	for i := 0; i < n; i++ {
		if idx = bytes.Index(b, sep); idx == -1 {
			break
		}
		b = b[idx+sepLen:]
		index += idx
	}
	if idx == -1 {
		index = -1
	} else {
		index += (n - 1) * sepLen
	}
	return
}

func ReplaceSlice(b []byte, start, end int, repl []byte) []byte {
	dst := make([]byte, 0, len(b)-end+start+len(repl))
	dst = append(dst, b[:start]...)
	dst = append(dst, repl...)
	dst = append(dst, b[end:]...)
	return dst
}

func clamp(a, start, end int) int {
	if a > end {
		return end
	}
	if a < start {
		return start
	}
	return a
}

// MultiError represents multiple errors.
type MultiError []error

func (e MultiError) Error() string {
	s := ""
	for _, err := range e {
		s += err.Error() + ","
	}
	return s
}

const (
	cmdId = iota
	pattId
	charId
	numId
	rangeId
	xId
	yId
	gId
	vId
	sId
	cId
	pId
	dId
	nId
	lId
	uId
	addrId
	commaId
	dotId
)

var grammar = p.Grammar("Sregex", map[string]p.Pattern{
	"Sregex": p.Concat(
		p.Or(
			p.NonTerm("Addr"),
			p.And(p.NonTerm("Command")),
			p.And(p.Any(0)),
		),
		p.Optional(p.NonTerm("Command")),
		p.Star(p.Concat(
			p.NonTerm("Pipe"),
			p.NonTerm("Command"),
		)),
		p.Not(p.Any(1)),
	),
	"Addr": p.Or(
		p.CapId(p.Literal(","), commaId),
		p.CapId(p.Literal("."), dotId),
		p.CapId(p.NonTerm("Pattern"), addrId),
	),
	"Pipe": p.Concat(
		p.NonTerm("S"),
		p.Literal("|"),
		p.NonTerm("S"),
	),
	"Command": p.CapId(p.Or(
		p.Concat(p.CapId(p.Literal("x"), xId), p.NonTerm("RCommand")),
		p.Concat(p.CapId(p.Literal("y"), yId), p.NonTerm("RCommand")),
		p.Concat(p.CapId(p.Literal("g"), gId), p.NonTerm("RCommand")),
		p.Concat(p.CapId(p.Literal("v"), vId), p.NonTerm("RCommand")),
		p.Concat(p.CapId(p.Literal("s"), sId), p.NonTerm("Pattern"), p.NonTerm("RPattern"), p.Optional(p.Literal("g"))),
		p.Concat(p.CapId(p.Literal("c"), cId), p.NonTerm("Pattern")),
		p.Concat(p.CapId(p.Literal("n"), nId), p.NonTerm("Range"), p.NonTerm("Command")),
		p.Concat(p.CapId(p.Literal("l"), lId), p.NonTerm("Range"), p.NonTerm("Command")),
		p.CapId(p.Literal("p"), pId),
		p.CapId(p.Literal("d"), dId),
		p.Concat(
			p.CapId(p.Set(charset.Range('a', 'z').Add(charset.Range('A', 'Z'))), uId),
			p.NonTerm("Pattern"),
		),
	), cmdId),
	"RCommand": p.Concat(
		p.NonTerm("Pattern"),
		p.NonTerm("S"),
		p.NonTerm("Command"),
	),
	"Pattern": p.Concat(
		p.Literal("/"),
		p.NonTerm("RPattern"),
	),
	"RPattern": p.Or(
		p.CapId(p.Concat(
			p.Star(p.Concat(
				p.Not(p.Literal("/")),
				p.NonTerm("Char"),
			)),
			p.Literal("/"),
		), pattId),
		p.Error("Pattern failed to match", nil),
	),
	"Range": p.CapId(p.Concat(
		p.Literal("["),
		p.NonTerm("Number"),
		p.Literal(":"),
		p.NonTerm("Number"),
		p.Literal("]"),
	), rangeId),
	"Char": p.CapId(p.Or(
		p.Concat(p.Literal("\\"), p.Set(charset.New([]byte{'/', 'n', 'r', 't', '\\'}))),
		p.Concat(p.Literal("\\"), p.Set(charset.Range('0', '2')), p.Set(charset.Range('0', '7')), p.Set(charset.Range('0', '7'))),
		p.Concat(p.Literal("\\"), p.Set(charset.Range('0', '7')), p.Optional(p.Set(charset.Range('0', '7')))),
		p.Concat(p.Not(p.Literal("\\")), p.Any(1)),
	), charId),
	"Number": p.CapId(p.Concat(
		p.Optional(p.Literal("-")),
		p.Plus(p.Set(charset.Range('0', '9'))),
	), numId),
	"S":     p.Star(p.NonTerm("Space")),
	"Space": p.Set(charset.New([]byte{9, 10, 11, 12, 13, ' '})),
})

func (r *SregxResult) ResolveAddr(b []byte, dot [2]int) (int, int, bool) {
	switch r.AddrType {
	case commaId:
		return 0, len(b), true
	case dotId:
		return dot[0], dot[1], true
	case addrId:
		re, err := regexp.Compile(r.Addr)
		if err != nil {
			return 0, 0, false
		}
		// Search after dot, wrap if not found
		loc := re.FindIndex(b[dot[1]:])
		if loc != nil {
			return dot[1] + loc[0], dot[1] + loc[1], true
		}
		loc = re.FindIndex(b[:dot[1]])
		if loc != nil {
			return loc[0], loc[1], true
		}
	}
	return 0, 0, false
}

type SregxResult struct {
	Cmd      Command
	Addr     string
	AddrType int // commaId, dotId, addrId, or 0
}

func SregxCompile(s string, out io.Writer) (*SregxResult, error) {
	peg := p.MustCompile(grammar)
	code := vm.Encode(peg)
	in := input.StringReader(s)
	machine := vm.NewVM(in, code)
	match, _, ast, errs := machine.Exec(memo.NoneTable{})
	if errs != nil {
		return nil, MultiError(errs)
	}
	if !match {
		return nil, MultiError{io.EOF}
	}

	inp := input.NewInput(in)
	res := &SregxResult{Cmd: None{}}

	startIdx := 0
	if len(ast) > 0 {
		first := ast[0]
		switch first.Id {
		case commaId, dotId, addrId:
			res.AddrType = int(first.Id)
			if first.Id == addrId {
				res.Addr = pattern(first.Children[0], inp)
			}
			startIdx = 1
		}
	}

	var cmds CommandPipeline
	for i := startIdx; i < len(ast); i++ {
		cmd, err := sregx_compile(ast[i], inp, out, nil)
		if err != nil {
			return nil, MultiError{err}
		}
		cmds = append(cmds, cmd)
	}

	if len(cmds) > 0 {
		res.Cmd = cmds
	}
	return res, nil
}

func sregx_compile(n *capture.Node, in *input.Input, out io.Writer, usrfns map[string]EvalMaker) (Command, error) {
	var c Command
	id := n.Children[0].Id
	switch id {
	case xId, yId, gId, vId, sId:
		regex, err := regexp.Compile(pattern(n.Children[1], in))
		if err != nil {
			return nil, err
		}
		if id == sId {
			c = S{
				Patt:    regex,
				Replace: []byte(pattern(n.Children[2], in)),
			}
		} else {
			cmd, err := sregx_compile(n.Children[2], in, out, usrfns)
			if err != nil {
				return nil, err
			}
			switch id {
			case xId:
				c = X{Patt: regex, Cmd: cmd}
			case yId:
				c = Y{Patt: regex, Cmd: cmd}
			case gId:
				c = G{Patt: regex, Cmd: cmd}
			case vId:
				c = V{Patt: regex, Cmd: cmd}
			}
		}
	case cId:
		c = C{Change: []byte(pattern(n.Children[1], in))}
	case nId, lId:
		start, end := rangeNums(n.Children[1], in)
		cmd, err := sregx_compile(n.Children[2], in, out, usrfns)
		if err != nil {
			return nil, err
		}
		if id == nId {
			c = N{Start: start, End: end, Cmd: cmd}
		} else {
			c = L{Start: start, End: end, Cmd: cmd}
		}
	case pId:
		c = P{W: out}
	case dId:
		c = D{}
	}
	return c, nil
}

var special = map[byte]byte{
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'\\': '\\',
	'/':  '/',
}

func char(b []byte) byte {
	if b[0] == '\\' {
		if v, ok := special[b[1]]; ok {
			return v
		}
		i, _ := strconv.ParseInt(string(b[1:]), 8, 8)
		return byte(i)
	}
	return b[0]
}

func pattern(n *capture.Node, in *input.Input) string {
	var bytes []byte
	for _, c := range n.Children {
		if c.Id == charId {
			bytes = append(bytes, char(in.Slice(c.Start(), c.End())))
		}
	}
	return string(bytes)
}

func rangeNums(n *capture.Node, in *input.Input) (int, int) {
	start, _ := strconv.Atoi(string(in.Slice(n.Children[0].Start(), n.Children[0].End())))
	end, _ := strconv.Atoi(string(in.Slice(n.Children[1].Start(), n.Children[1].End())))
	return start, end
}

type EvalMaker func(s string) (Evaluator, error)
