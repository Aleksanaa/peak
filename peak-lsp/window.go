package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/aleksana/peak/internal/wevent"
	enry "github.com/go-enry/go-enry/v2"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func watchWindow(fs afero.Fs, id int, retitleCh <-chan string) {
	base := fmt.Sprintf("/%d", id)

	tag, err := afero.ReadFile(fs, base+"/tag")
	if err != nil {
		return
	}
	filename := extractFilename(string(tag))

	eventF, err := fs.OpenFile(base+"/event", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer eventF.Close()

	type state struct {
		hl   *gotreesitter.Highlighter
		lang string
		tree *gotreesitter.Tree
		snap []byte // first ~8K of body for content-based re-detection
	}

	var (
		mu      sync.Mutex
		cur     = state{lang: "", hl: detectHighlighter(filename, nil)}
		curFile = filename
	)
	if cur.hl != nil {
		cur.lang = enry.GetLanguage(filename, nil)
	}

	trigger := make(chan struct{}, 1)
	signal := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	done := make(chan struct{})

	// highlight worker
	go func() {
		defer close(done)
		for range trigger {
			body, err := afero.ReadFile(fs, base+"/body")
			if err != nil || len(body) == 0 {
				continue
			}

			mu.Lock()
			s := cur
			if s.snap == nil {
				// First body read: refine detection with content.
				snap := body
				if len(snap) > 8192 {
					snap = snap[:8192]
				}
				cur.snap = snap
				lang := enry.GetLanguage(curFile, snap)
				if lang != "" && lang != s.lang {
					if hl := buildHighlighterForLang(lang); hl != nil {
						cur.hl = hl
						cur.lang = lang
						cur.tree = nil
					}
				}
				s = cur
			}
			mu.Unlock()

			if s.hl == nil {
				writeColorSpans(fs, base, nil, nil)
				continue
			}

			ranges, next := s.hl.HighlightIncremental(body, s.tree)

			mu.Lock()
			cur.tree = next
			mu.Unlock()

			writeColorSpans(fs, base, body, ranges)
		}
	}()

	// retitle watcher: re-detect language when the window's file changes.
	go func() {
		for {
			select {
			case newFilename, ok := <-retitleCh:
				if !ok {
					return
				}
				mu.Lock()
				curFile = newFilename
				snap := cur.snap
				mu.Unlock()

				lang := enry.GetLanguage(newFilename, snap)
				hl := buildHighlighterForLang(lang)

				mu.Lock()
				if lang != cur.lang {
					cur.hl = hl
					cur.lang = lang
					cur.tree = nil
				}
				mu.Unlock()

				signal()
			case <-done:
				return
			}
		}
	}()

	// Initial highlight pass.
	signal()

	br := bufio.NewReader(eventF)
	for {
		ev, err := wevent.Read(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		switch ev.Type {
		case 'I', 'D':
			signal()
		case 'x', 'l':
			// Peak processes x/l events directly; no write-back needed.
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

// detectHighlighter detects the language using go-enry and builds a highlighter.
// content may be nil for filename-only detection.
func detectHighlighter(filename string, content []byte) *gotreesitter.Highlighter {
	lang := enry.GetLanguage(filename, content)
	if lang == "" {
		return nil
	}
	return buildHighlighterForLang(lang)
}

// buildHighlighterForLang creates a tree-sitter Highlighter for the given
// go-enry / linguist language name, or nil if the language is not supported.
func buildHighlighterForLang(lang string) *gotreesitter.Highlighter {
	if lang == "" {
		return nil
	}
	entry := grammars.DetectLanguageByName(lang)
	if entry == nil {
		return nil
	}
	l := entry.Language()
	if l == nil {
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
			return factory(src, l)
		}))
	}

	hl, err := gotreesitter.NewHighlighter(l, query, opts...)
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
