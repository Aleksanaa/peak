package main

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// colorPair is the background and foreground of one kind of text area.
type colorPair struct{ BG, FG tcell.Color }

func (c *colorPair) style() tcell.Style {
	return tcell.StyleDefault.Background(c.BG).Foreground(c.FG)
}

type Theme struct {
	GlobalTag, ColTag, Tag, Body, Selection colorPair

	Handle, ScrollThumb, ScrollGutter tcell.Color
	HandleDirty, HandleError          tcell.Color
	HandleWritable, HandleUnwritable  tcell.Color
	HandleColumn                      tcell.Color

	// Syn colors text by the attribute its span names, as "keyword".
	Syn map[string]tcell.Color
}

// fields maps the keys of a theme file to the colors they set, but for
// the syntax colors: a key SynX sets Syn["x"].
func (t *Theme) fields() map[string]*tcell.Color {
	return map[string]*tcell.Color{
		"GlobalTagBG":      &t.GlobalTag.BG,
		"GlobalTagFG":      &t.GlobalTag.FG,
		"ColTagBG":         &t.ColTag.BG,
		"ColTagFG":         &t.ColTag.FG,
		"TagBG":            &t.Tag.BG,
		"TagFG":            &t.Tag.FG,
		"BodyBG":           &t.Body.BG,
		"BodyFG":           &t.Body.FG,
		"SelectionBG":      &t.Selection.BG,
		"SelectionFG":      &t.Selection.FG,
		"Handle":           &t.Handle,
		"ScrollThumb":      &t.ScrollThumb,
		"ScrollGutter":     &t.ScrollGutter,
		"HandleDirty":      &t.HandleDirty,
		"HandleError":      &t.HandleError,
		"HandleWritable":   &t.HandleWritable,
		"HandleUnwritable": &t.HandleUnwritable,
		"HandleColumn":     &t.HandleColumn,
	}
}

// ApplyTheme sets the colors the theme file /peak/theme/name gives, one
// "key\t0xrrggbb" per line.
func (e *Editor) ApplyTheme(name string) error {
	data, err := readFile("/peak/theme/" + name)
	if err != nil {
		return err
	}
	t := &e.theme
	if t.Syn == nil {
		t.Syn = map[string]tcell.Color{}
	}
	fields := t.fields()
	for line := range strings.SplitSeq(string(data), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "\t")
		hex, err := strconv.ParseUint(val, 0, 32)
		if !ok || err != nil {
			continue
		}
		c := color.NewHexColor(int32(hex))
		if attr, ok := strings.CutPrefix(key, "Syn"); ok {
			t.Syn[strings.ToLower(attr)] = c
		} else if p := fields[key]; p != nil {
			*p = c
		}
	}
	return nil
}
