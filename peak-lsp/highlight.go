package main

import (
	"github.com/aleksana/peak/internal/wevent"
	"github.com/odvcencio/gotreesitter"
)

type contentEdit struct {
	body    []byte
	edit    gotreesitter.InputEdit
	changed bool
}

type highlightState struct {
	hl   *gotreesitter.Highlighter
	lang string
	tree *gotreesitter.Tree
	snap []byte
	body []byte
}

func resetIncrementalState(cur *highlightState) {
	resetIncrementalTree(cur)
	cur.body = nil
}

func resetIncrementalTree(cur *highlightState) {
	if cur.tree != nil {
		cur.tree.Release()
	}
	cur.tree = nil
}

func resetAfterUnknownBodyChange(cur *highlightState) {
	resetIncrementalState(cur)
	cur.snap = nil
}

func applyContentEdit(body []byte, ev wevent.Event) (contentEdit, bool) {
	if ev.Origin != 'K' {
		return contentEdit{}, false
	}
	switch ev.Type {
	case 'I':
		if ev.Q0 != ev.Q1 {
			return contentEdit{}, false
		}
		if ev.Text == "" {
			return contentEdit{body: append([]byte(nil), body...), changed: false}, true
		}
		startByte, ok := runeToByteOffset(body, ev.Q0)
		if !ok || !fitsUint32(startByte) || !fitsUint32(startByte+len(ev.Text)) {
			return contentEdit{}, false
		}
		startPoint, ok := pointAtByte(body, startByte)
		if !ok {
			return contentEdit{}, false
		}
		inserted := []byte(ev.Text)
		next := make([]byte, 0, len(body)+len(inserted))
		next = append(next, body[:startByte]...)
		next = append(next, inserted...)
		next = append(next, body[startByte:]...)
		edit := gotreesitter.InputEdit{
			StartByte:   uint32(startByte),
			OldEndByte:  uint32(startByte),
			NewEndByte:  uint32(startByte + len(inserted)),
			StartPoint:  startPoint,
			OldEndPoint: startPoint,
			NewEndPoint: advancePoint(startPoint, inserted),
		}
		return contentEdit{body: next, edit: edit, changed: true}, true
	case 'D':
		if ev.Q0 > ev.Q1 {
			return contentEdit{}, false
		}
		startByte, ok := runeToByteOffset(body, ev.Q0)
		if !ok || !fitsUint32(startByte) {
			return contentEdit{}, false
		}
		oldEndByte, ok := runeToByteOffset(body, ev.Q1)
		if !ok || !fitsUint32(oldEndByte) {
			return contentEdit{}, false
		}
		startPoint, ok := pointAtByte(body, startByte)
		if !ok {
			return contentEdit{}, false
		}
		oldEndPoint, ok := pointAtByte(body, oldEndByte)
		if !ok {
			return contentEdit{}, false
		}
		if startByte == oldEndByte {
			return contentEdit{body: append([]byte(nil), body...), changed: false}, true
		}
		next := make([]byte, 0, len(body)-(oldEndByte-startByte))
		next = append(next, body[:startByte]...)
		next = append(next, body[oldEndByte:]...)
		edit := gotreesitter.InputEdit{
			StartByte:   uint32(startByte),
			OldEndByte:  uint32(oldEndByte),
			NewEndByte:  uint32(startByte),
			StartPoint:  startPoint,
			OldEndPoint: oldEndPoint,
			NewEndPoint: startPoint,
		}
		return contentEdit{body: next, edit: edit, changed: true}, true
	default:
		return contentEdit{}, false
	}
}

func applyEventToIncrementalState(cur *highlightState, ev wevent.Event) bool {
	if cur.body == nil {
		resetIncrementalState(cur)
		return false
	}
	result, ok := applyContentEdit(cur.body, ev)
	if !ok {
		resetIncrementalState(cur)
		return false
	}
	if result.changed && cur.tree != nil {
		cur.tree.Edit(result.edit)
	}
	cur.body = result.body
	return true
}
