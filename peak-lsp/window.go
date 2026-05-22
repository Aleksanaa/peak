package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func watchWindow(fs afero.Fs, id int) {
	base := fmt.Sprintf("/%d", id)

	tag, err := afero.ReadFile(fs, base+"/tag")
	if err != nil {
		return
	}
	filename := extractFilename(string(tag))

	hl := buildHighlighter(filename)
	if hl == nil {
		return
	}

	eventF, err := fs.OpenFile(base+"/event", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer eventF.Close()

	var (
		treeMu sync.Mutex
		tree   *gotreesitter.Tree
	)

	// trigger is a size-1 channel. Sending on it requests a highlight pass;
	// if one is already pending the send is dropped, coalescing rapid edits.
	trigger := make(chan struct{}, 1)

	go func() {
		for range trigger {
			body, err := afero.ReadFile(fs, base+"/body")
			if err != nil || len(body) == 0 {
				continue
			}
			treeMu.Lock()
			prev := tree
			treeMu.Unlock()

			ranges, next := hl.HighlightIncremental(body, prev)

			treeMu.Lock()
			tree = next
			treeMu.Unlock()

			writeColorSpans(fs, base, body, ranges)
		}
	}()

	// Initial highlight pass.
	trigger <- struct{}{}

	signal := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	scanner := bufio.NewScanner(eventF)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "I "), strings.HasPrefix(line, "D "):
			signal()
		case strings.HasPrefix(line, "x "), strings.HasPrefix(line, "l "):
			// Write back unhandled execute/look events so the editor acts on them.
			eventF.WriteString(line + "\n")
		}
	}
	close(trigger)
}

// extractFilename returns the first whitespace-delimited token from a tag string.
func extractFilename(tag string) string {
	f := strings.Fields(tag)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// buildHighlighter creates a tree-sitter Highlighter for the given filename,
// or nil if the language is not recognised.
func buildHighlighter(filename string) *gotreesitter.Highlighter {
	entry := grammars.DetectLanguage(filename)
	if entry == nil {
		return nil
	}
	lang := entry.Language()
	if lang == nil {
		return nil
	}

	query := entry.HighlightQuery
	if query == "" {
		return nil
	}

	var opts []gotreesitter.HighlighterOption
	if entry.TokenSourceFactory != nil {
		factory := entry.TokenSourceFactory
		opts = append(opts, gotreesitter.WithTokenSourceFactory(func(src []byte) gotreesitter.TokenSource {
			return factory(src, lang)
		}))
	}

	hl, err := gotreesitter.NewHighlighter(lang, query, opts...)
	if err != nil {
		return nil
	}
	return hl
}

// writeColorSpans converts highlight ranges to rune-offset color spans and
// writes them to the window's color file. It always opens and closes the file
// so that an empty result clears stale spans from a previous highlight pass.
func writeColorSpans(fs afero.Fs, base string, body []byte, ranges []gotreesitter.HighlightRange) {
	colorF, err := fs.OpenFile(base+"/color", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer colorF.Close()

	if len(ranges) == 0 {
		return
	}

	byteToRune := buildByteToRune(body)

	var sb strings.Builder
	for _, r := range ranges {
		attr := captureToAttr(r.Capture)
		if attr == "" {
			continue
		}
		start := int(r.StartByte)
		end := int(r.EndByte)
		if start >= len(byteToRune) || end > len(byteToRune) {
			continue
		}
		q0, q1 := byteToRune[start], byteToRune[end]
		if q0 >= q1 {
			continue
		}
		fmt.Fprintf(&sb, "%d %d %s\n", q0, q1, attr)
	}
	if sb.Len() > 0 {
		colorF.WriteString(sb.String())
	}
}

// buildByteToRune builds a slice where index i holds the rune offset
// corresponding to byte offset i in src. Index len(src) is the past-the-end sentinel.
func buildByteToRune(src []byte) []int {
	out := make([]int, len(src)+1)
	runeOff := 0
	for i := 0; i < len(src); {
		_, size := utf8.DecodeRune(src[i:])
		for j := 0; j < size; j++ {
			out[i+j] = runeOff
		}
		i += size
		runeOff++
	}
	out[len(src)] = runeOff
	return out
}

// captureToAttr maps a tree-sitter capture name to a peak colour attribute.
// Returns "" to skip captures with no useful mapping.
func captureToAttr(capture string) string {
	name := capture
	if dot := strings.IndexByte(name, '.'); dot != -1 {
		name = name[:dot]
	}
	switch name {
	case "keyword", "conditional", "repeat", "include", "exception", "label":
		return "keyword"
	case "type", "storageclass", "structure":
		return "type"
	case "comment":
		return "comment"
	case "string", "character":
		return "string"
	case "number", "float", "integer", "boolean":
		return "number"
	case "function", "method", "builtin":
		return "function"
	case "operator", "punctuation":
		return "operator"
	case "variable", "parameter", "field", "property", "namespace", "attribute":
		return "variable"
	case "constant":
		return "constant"
	default:
		return ""
	}
}
