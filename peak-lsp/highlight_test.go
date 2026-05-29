package main

import (
	"bytes"
	"testing"

	"github.com/aleksana/peak/internal/wevent"
	"github.com/odvcencio/gotreesitter"
)

func TestApplyContentEditInsertAtBeginning(t *testing.T) {
	result, ok := applyContentEdit([]byte("world"), wevent.Event{Origin: 'K', Type: 'I', Q0: 0, Q1: 0, Text: "hello\n"})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "hello\nworld"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	want := gotreesitter.InputEdit{
		StartByte:   0,
		OldEndByte:  0,
		NewEndByte:  6,
		StartPoint:  gotreesitter.Point{Row: 0, Column: 0},
		OldEndPoint: gotreesitter.Point{Row: 0, Column: 0},
		NewEndPoint: gotreesitter.Point{Row: 1, Column: 0},
	}
	if result.edit != want || !result.changed {
		t.Fatalf("result = %#v, want edit %#v with changed=true", result, want)
	}
}

func TestApplyContentEditInsertAfterMultibyte(t *testing.T) {
	result, ok := applyContentEdit([]byte("aéb"), wevent.Event{Origin: 'K', Type: 'I', Q0: 2, Q1: 2, Text: "X"})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "aéXb"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	want := gotreesitter.InputEdit{
		StartByte:   3,
		OldEndByte:  3,
		NewEndByte:  4,
		StartPoint:  gotreesitter.Point{Row: 0, Column: 3},
		OldEndPoint: gotreesitter.Point{Row: 0, Column: 3},
		NewEndPoint: gotreesitter.Point{Row: 0, Column: 4},
	}
	if result.edit != want || !result.changed {
		t.Fatalf("result = %#v, want edit %#v with changed=true", result, want)
	}
}

func TestApplyContentEditInsertAtEnd(t *testing.T) {
	result, ok := applyContentEdit([]byte("abc"), wevent.Event{Origin: 'K', Type: 'I', Q0: 3, Q1: 3, Text: "de"})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "abcde"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if !result.changed {
		t.Fatal("changed = false, want true")
	}
	want := gotreesitter.InputEdit{
		StartByte:   3,
		OldEndByte:  3,
		NewEndByte:  5,
		StartPoint:  gotreesitter.Point{Row: 0, Column: 3},
		OldEndPoint: gotreesitter.Point{Row: 0, Column: 3},
		NewEndPoint: gotreesitter.Point{Row: 0, Column: 5},
	}
	if result.edit != want {
		t.Fatalf("edit = %#v, want %#v", result.edit, want)
	}
}

func TestApplyContentEditInsertEmptyText(t *testing.T) {
	result, ok := applyContentEdit([]byte("abc"), wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: ""})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "abc"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if result.changed {
		t.Fatal("changed = true, want false for empty insert")
	}
}

func TestApplyContentEditInsertAcrossLines(t *testing.T) {
	result, ok := applyContentEdit([]byte("ab\ncd"), wevent.Event{Origin: 'K', Type: 'I', Q0: 3, Q1: 3, Text: "X\nY"})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "ab\nX\nYcd"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	want := gotreesitter.InputEdit{
		StartByte:   3,
		OldEndByte:  3,
		NewEndByte:  6,
		StartPoint:  gotreesitter.Point{Row: 1, Column: 0},
		OldEndPoint: gotreesitter.Point{Row: 1, Column: 0},
		NewEndPoint: gotreesitter.Point{Row: 2, Column: 1},
	}
	if result.edit != want || !result.changed {
		t.Fatalf("result = %#v, want edit %#v with changed=true", result, want)
	}
}

func TestApplyContentEditDeleteSingleLine(t *testing.T) {
	result, ok := applyContentEdit([]byte("abcdef"), wevent.Event{Origin: 'K', Type: 'D', Q0: 2, Q1: 5})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "abf"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	want := gotreesitter.InputEdit{
		StartByte:   2,
		OldEndByte:  5,
		NewEndByte:  2,
		StartPoint:  gotreesitter.Point{Row: 0, Column: 2},
		OldEndPoint: gotreesitter.Point{Row: 0, Column: 5},
		NewEndPoint: gotreesitter.Point{Row: 0, Column: 2},
	}
	if result.edit != want || !result.changed {
		t.Fatalf("result = %#v, want edit %#v with changed=true", result, want)
	}
}

func TestApplyContentEditDeleteMultilineAndMultibyte(t *testing.T) {
	result, ok := applyContentEdit([]byte("aé\n日b\ncz"), wevent.Event{Origin: 'K', Type: 'D', Q0: 1, Q1: 7})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "az"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	want := gotreesitter.InputEdit{
		StartByte:   1,
		OldEndByte:  10,
		NewEndByte:  1,
		StartPoint:  gotreesitter.Point{Row: 0, Column: 1},
		OldEndPoint: gotreesitter.Point{Row: 2, Column: 1},
		NewEndPoint: gotreesitter.Point{Row: 0, Column: 1},
	}
	if result.edit != want || !result.changed {
		t.Fatalf("result = %#v, want edit %#v with changed=true", result, want)
	}
}

func TestApplyContentEditDeleteEntireBody(t *testing.T) {
	result, ok := applyContentEdit([]byte("abc"), wevent.Event{Origin: 'K', Type: 'D', Q0: 0, Q1: 3})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if len(result.body) != 0 {
		t.Fatalf("body = %q, want empty", result.body)
	}
	if !result.changed {
		t.Fatal("changed = false, want true")
	}
}

func TestApplyContentEditDeleteNoop(t *testing.T) {
	result, ok := applyContentEdit([]byte("abc"), wevent.Event{Origin: 'K', Type: 'D', Q0: 1, Q1: 1})
	if !ok {
		t.Fatal("applyContentEdit returned false")
	}
	if got, want := string(result.body), "abc"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if result.changed {
		t.Fatal("changed = true, want false for no-op delete")
	}
}

func TestApplyContentEditRejectsInvalidEvents(t *testing.T) {
	body := []byte("abc")
	tests := []wevent.Event{
		{Origin: 'M', Type: 'I', Q0: 1, Text: "x"},
		{Origin: 'K', Type: 'x', Q0: 1, Text: "x"},
		{Origin: 'K', Type: 'I', Q0: 4, Text: "x"},
		{Origin: 'K', Type: 'I', Q0: 1, Q1: 2, Text: "x"},
		{Origin: 'K', Type: 'D', Q0: 2, Q1: 4},
		{Origin: 'K', Type: 'D', Q0: 3, Q1: 2},
	}
	for _, ev := range tests {
		if result, ok := applyContentEdit(body, ev); ok {
			t.Fatalf("applyContentEdit(%#v) = %#v, true; want false", ev, result)
		}
	}
}

func TestApplyEventToIncrementalStateUpdatesMirror(t *testing.T) {
	cur := &highlightState{body: []byte("abc")}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: "X"}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "aXbc"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.tree != nil {
		t.Fatal("cur.tree changed from nil")
	}
}

func TestApplyEventToIncrementalStateChangingInsertIncrementsGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: "X"}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "aXbc"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.gen != 6 {
		t.Fatalf("gen = %d, want 6 after changing insert", cur.gen)
	}
}

func TestApplyEventToIncrementalStateChangingDeleteIncrementsGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'D', Q0: 1, Q1: 2}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "ac"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.gen != 6 {
		t.Fatalf("gen = %d, want 6 after changing delete", cur.gen)
	}
}

func TestApplyEventToIncrementalStateNoopInsertKeepsGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: ""}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "abc"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.gen != 5 {
		t.Fatalf("gen = %d, want 5 after no-op insert", cur.gen)
	}
}

func TestApplyEventToIncrementalStateNoopDeleteKeepsGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'D', Q0: 1, Q1: 1}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "abc"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.gen != 5 {
		t.Fatalf("gen = %d, want 5 after no-op delete", cur.gen)
	}
}

func TestApplyEventToIncrementalStateRejectsUninitializedMirror(t *testing.T) {
	cur := &highlightState{tree: &gotreesitter.Tree{}}
	if applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 0, Q1: 0, Text: "x"}) {
		t.Fatal("applyEventToIncrementalState returned true, want false")
	}
	if cur.body != nil || cur.tree != nil {
		t.Fatalf("state after reset = %#v, want nil body and tree", cur)
	}
}

func TestApplyEventToIncrementalStateResetsOnInvalidOffset(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), tree: &gotreesitter.Tree{}}
	if applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'D', Q0: 2, Q1: 10}) {
		t.Fatal("applyEventToIncrementalState returned true, want false")
	}
	if cur.body != nil || cur.tree != nil {
		t.Fatalf("state after reset = %#v, want nil body and tree", cur)
	}
}

func TestApplyEventToIncrementalStateEditsTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{body: []byte("x"), tree: tree}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: "Y"}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if got, want := string(cur.body), "xY"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if cur.tree == nil {
		t.Fatal("cur.tree is nil, want non-nil after edit")
	}
}

func TestResetIncrementalStateKeepsHighlighterAndLanguage(t *testing.T) {
	cur := &highlightState{lang: "Go", body: []byte("abc"), tree: &gotreesitter.Tree{}, snap: []byte("ab")}
	resetIncrementalState(cur)
	if cur.body != nil || cur.tree != nil {
		t.Fatalf("state after reset = %#v, want nil body and tree", cur)
	}
	if cur.lang != "Go" || string(cur.snap) != "ab" {
		t.Fatalf("reset changed language detection fields: %#v", cur)
	}
}

func testTreeForRelease() *gotreesitter.Tree {
	leaf := gotreesitter.NewLeafNode(
		gotreesitter.Symbol(1),
		true,
		0,
		1,
		gotreesitter.Point{Row: 0, Column: 0},
		gotreesitter.Point{Row: 0, Column: 1},
	)
	root := gotreesitter.NewParentNode(gotreesitter.Symbol(2), true, []*gotreesitter.Node{leaf}, nil, 0)
	return gotreesitter.NewTree(root, []byte("x"), &gotreesitter.Language{Name: "test"})
}

func TestResetIncrementalStateReleasesTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{body: []byte("x"), tree: tree}
	resetIncrementalState(cur)
	if cur.tree != nil {
		t.Fatalf("cur.tree = %#v, want nil", cur.tree)
	}
	if tree.RootNode() != nil {
		t.Fatal("tree root still present after reset; want released tree")
	}
}

func TestResetIncrementalTreeKeepsBodyAndReleasesTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{body: []byte("x"), tree: tree}
	resetIncrementalTree(cur)
	if got, want := string(cur.body), "x"; got != want {
		t.Fatalf("cur.body = %q, want %q", got, want)
	}
	if cur.tree != nil {
		t.Fatalf("cur.tree = %#v, want nil", cur.tree)
	}
	if tree.RootNode() != nil {
		t.Fatal("tree root still present after tree reset; want released tree")
	}
}

func TestResetIncrementalStateNilTreeNoop(t *testing.T) {
	cur := &highlightState{body: []byte("abc")}
	resetIncrementalState(cur)
	if cur.body != nil {
		t.Fatalf("body = %v, want nil", cur.body)
	}
	if cur.tree != nil {
		t.Fatalf("tree = %v, want nil", cur.tree)
	}
}

func TestResetAfterUnknownBodyChangeClearsBodyTreeAndSnap(t *testing.T) {
	cur := &highlightState{
		body: []byte("old"),
		tree: testTreeForRelease(),
		snap: []byte("old"),
		lang: "Go",
	}
	resetAfterUnknownBodyChange(cur)
	if cur.body != nil || cur.tree != nil || cur.snap != nil {
		t.Fatalf("state after reset = %#v, want nil body/tree/snap", cur)
	}
	if cur.lang != "Go" {
		t.Fatalf("lang = %q, want Go", cur.lang)
	}
}

func TestOrdinaryInsertKeepsMirrorAndAdvancesGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), tree: testTreeForRelease(), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 1, Q1: 1, Text: "X"}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if cur.body == nil {
		t.Fatal("body is nil after ordinary insert; should remain set")
	}
	if cur.tree == nil {
		t.Fatal("tree is nil after ordinary insert; should remain set")
	}
	if cur.gen != 6 {
		t.Fatalf("gen = %d, want 6 after ordinary insert", cur.gen)
	}
}

func TestOrdinaryDeleteKeepsMirrorAndAdvancesGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), tree: testTreeForRelease(), gen: 5}
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'D', Q0: 1, Q1: 2}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	if cur.body == nil {
		t.Fatal("body is nil after ordinary delete; should remain set")
	}
	if cur.tree == nil {
		t.Fatal("tree is nil after ordinary delete; should remain set")
	}
	if cur.gen != 6 {
		t.Fatalf("gen = %d, want 6 after ordinary delete", cur.gen)
	}
}

func TestInvalidEditIncrementsGen(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), tree: testTreeForRelease(), gen: 5}
	if applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'D', Q0: 2, Q1: 10}) {
		t.Fatal("applyEventToIncrementalState returned true for invalid edit")
	}
	if cur.gen != 6 {
		t.Fatalf("gen = %d, want 6 (incremented by invalid edit)", cur.gen)
	}
	if cur.body != nil || cur.tree != nil {
		t.Fatalf("state after invalid edit = body=%q tree=%v; want nil/nil", cur.body, cur.tree)
	}
}

func TestInvalidateHighlightStateIncrementsGenAndClears(t *testing.T) {
	cur := &highlightState{
		body: []byte("old"),
		tree: testTreeForRelease(),
		snap: []byte("ol"),
		gen:  3,
		lang: "Go",
	}
	invalidateHighlightState(cur)
	if cur.gen != 4 {
		t.Fatalf("gen = %d, want 4", cur.gen)
	}
	if cur.body != nil || cur.tree != nil {
		t.Fatalf("state after invalidate = body=%q tree=%v; want nil/nil", cur.body, cur.tree)
	}
	if cur.lang != "Go" {
		t.Fatalf("lang = %q, want Go", cur.lang)
	}
}

func TestInvalidateHighlightStateAlsoClearsSnap(t *testing.T) {
	cur := &highlightState{
		body: []byte("old"),
		tree: testTreeForRelease(),
		snap: []byte("ol"),
		gen:  3,
		lang: "Go",
	}
	invalidateHighlightState(cur)
	if cur.snap != nil {
		t.Fatalf("snap = %q, want nil after invalidate", cur.snap)
	}
}

func TestSnapshotHighlightStateCopiesBodyAndTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{
		body: []byte("hello"),
		tree: tree,
		hl:   &gotreesitter.Highlighter{},
		gen:  7,
		lang: "Go",
	}
	snap := snapshotHighlightState(cur)

	if !bytes.Equal(snap.body, []byte("hello")) {
		t.Fatalf("snap.body = %q, want %q", snap.body, "hello")
	}
	if snap.gen != 7 {
		t.Fatalf("snap.gen = %d, want 7", snap.gen)
	}
	if snap.tree == nil {
		t.Fatal("snap.tree is nil, want a copy of the original tree")
	}
	if snap.hl == nil {
		t.Fatal("snap.hl is nil, want the highlighter")
	}
}

func TestSnapshotHighlightStateKeepsOriginalTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{
		body: []byte("hello"),
		tree: tree,
		gen:  7,
	}
	snapshotHighlightState(cur)

	if cur.tree != tree {
		t.Fatal("cur.tree should keep the original tree after snapshot")
	}
	if cur.body == nil {
		t.Fatal("cur.body should not be nil after snapshot")
	}
}

func TestSnapshotHighlightStateWithNilTree(t *testing.T) {
	cur := &highlightState{
		body: []byte("hello"),
		tree: nil,
		gen:  2,
	}
	snap := snapshotHighlightState(cur)

	if snap.tree != nil {
		t.Fatal("snap.tree should be nil when cur.tree was nil")
	}
	if !bytes.Equal(snap.body, []byte("hello")) {
		t.Fatalf("snap.body = %q, want %q", snap.body, "hello")
	}
}

func TestSnapshotBodyIsIndependentCopy(t *testing.T) {
	cur := &highlightState{
		body: []byte("abc"),
		tree: nil,
		gen:  1,
	}
	snap := snapshotHighlightState(cur)
	cur.body[0] = 'X'

	if snap.body[0] != 'a' {
		t.Fatal("modifying cur.body affected snap.body; body was not copied")
	}
}

func TestCommitHighlightTreeSuccess(t *testing.T) {
	oldTree := testTreeForRelease()
	cur := &highlightState{body: []byte("abc"), tree: oldTree, gen: 5}
	snap := highlightSnapshot{body: []byte("abc"), gen: 5}
	newTree := testTreeForRelease()

	if !commitHighlightTree(cur, newTree, snap) {
		t.Fatal("commitHighlightTree returned false, want true")
	}
	if cur.tree != newTree {
		t.Fatal("cur.tree was not set to the committed tree")
	}
	if oldTree.RootNode() != nil {
		t.Fatal("old tree should have been released after successful replacement")
	}
}

func TestCommitHighlightTreeKeepsSameTree(t *testing.T) {
	tree := testTreeForRelease()
	cur := &highlightState{body: []byte("abc"), tree: tree, gen: 5}
	snap := highlightSnapshot{body: []byte("abc"), gen: 5}

	if !commitHighlightTree(cur, tree, snap) {
		t.Fatal("commitHighlightTree returned false, want true")
	}
	if cur.tree != tree {
		t.Fatal("cur.tree changed when committing the same tree")
	}
	if tree.RootNode() == nil {
		t.Fatal("same tree should not have been released")
	}
}

func TestCommitHighlightTreeRejectsGenMismatch(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 99}
	snap := highlightSnapshot{body: []byte("abc"), gen: 5}
	newTree := testTreeForRelease()

	if commitHighlightTree(cur, newTree, snap) {
		t.Fatal("commitHighlightTree returned true on gen mismatch, want false")
	}
	if cur.tree != nil {
		t.Fatal("cur.tree should be nil after rejected commit")
	}
	if newTree.RootNode() != nil {
		t.Fatal("rejected tree should have been released")
	}
}

func TestCommitHighlightTreeRejectsSnapshotAfterLaterEdit(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	snap := snapshotHighlightState(cur)
	if !applyEventToIncrementalState(cur, wevent.Event{Origin: 'K', Type: 'I', Q0: 3, Q1: 3, Text: "X"}) {
		t.Fatal("applyEventToIncrementalState returned false")
	}
	newTree := testTreeForRelease()
	if commitHighlightTree(cur, newTree, snap) {
		t.Fatal("commitHighlightTree returned true for snapshot from older generation")
	}
	if cur.tree != nil {
		t.Fatal("cur.tree should remain nil after rejected stale commit")
	}
	if newTree.RootNode() != nil {
		t.Fatal("rejected tree should have been released")
	}
}

func TestCommitHighlightTreeRejectsBodyChange(t *testing.T) {
	cur := &highlightState{body: []byte("abcXYZ"), gen: 5}
	snap := highlightSnapshot{body: []byte("abc"), gen: 5}
	newTree := testTreeForRelease()

	if commitHighlightTree(cur, newTree, snap) {
		t.Fatal("commitHighlightTree returned true on body change, want false")
	}
	if cur.tree != nil {
		t.Fatal("cur.tree should be nil after rejected commit")
	}
	if newTree.RootNode() != nil {
		t.Fatal("rejected tree should have been released")
	}
}

func TestCommitHighlightTreeWithNilTree(t *testing.T) {
	cur := &highlightState{body: []byte("abc"), gen: 5}
	snap := highlightSnapshot{body: []byte("abc"), gen: 5}

	if !commitHighlightTree(cur, nil, snap) {
		t.Fatal("commitHighlightTree returned false for nil tree commit")
	}
	if cur.tree != nil {
		t.Fatal("cur.tree should be nil after committing nil tree")
	}
}

func TestReleaseSnapshotTreeReleasesCopy(t *testing.T) {
	tree := testTreeForRelease()
	snap := highlightSnapshot{tree: tree}
	releaseSnapshotTree(snap)
	if tree.RootNode() != nil {
		t.Fatal("snapshot tree root still present after release")
	}
}

func TestReleaseSnapshotTreeNilNoop(t *testing.T) {
	releaseSnapshotTree(highlightSnapshot{})
}

func TestBuildColorSpanText(t *testing.T) {
	body := []byte("package main\nvar café = 1\n")
	ranges := []gotreesitter.HighlightRange{
		{StartByte: 0, EndByte: 7, Capture: "keyword"},
		{StartByte: 13, EndByte: 16, Capture: "keyword"},
		{StartByte: 17, EndByte: 22, Capture: "variable"},
		{StartByte: 25, EndByte: 26, Capture: "number"},
	}

	got := buildColorSpanText(body, ranges)
	want := "0 7 keyword\n13 16 keyword\n17 21 variable\n24 25 number\n"
	if got != want {
		t.Fatalf("buildColorSpanText() = %q, want %q", got, want)
	}
}

func TestBuildColorSpanTextSkipsInvalidAndUnknownRanges(t *testing.T) {
	body := []byte("abc")
	ranges := []gotreesitter.HighlightRange{
		{StartByte: 0, EndByte: 1, Capture: "unknown_capture"},
		{StartByte: 2, EndByte: 2, Capture: "keyword"},
		{StartByte: 0, EndByte: 4, Capture: "keyword"},
		{StartByte: 1, EndByte: 3, Capture: "string"},
	}

	got := buildColorSpanText(body, ranges)
	want := "1 3 string\n"
	if got != want {
		t.Fatalf("buildColorSpanText() = %q, want %q", got, want)
	}
}

func TestCaptureToAttr(t *testing.T) {
	tests := []struct {
		capture string
		want    string
	}{
		{"keyword", "keyword"},
		{"conditional", "keyword"},
		{"repeat", "keyword"},
		{"include", "keyword"},
		{"exception", "keyword"},
		{"label", "keyword"},
		{"keyword.special", "keyword"},
		{"type", "type"},
		{"storageclass", "type"},
		{"structure", "type"},
		{"comment", "comment"},
		{"comment.line", "comment"},
		{"string", "string"},
		{"character", "string"},
		{"number", "number"},
		{"float", "number"},
		{"integer", "number"},
		{"boolean", "number"},
		{"function", "function"},
		{"method", "function"},
		{"builtin", "function"},
		{"operator", "operator"},
		{"punctuation", "operator"},
		{"variable", "variable"},
		{"parameter", "variable"},
		{"field", "variable"},
		{"property", "variable"},
		{"namespace", "variable"},
		{"attribute", "variable"},
		{"constant", "constant"},
		{"unknown_capture", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.capture, func(t *testing.T) {
			if got := captureToAttr(tt.capture); got != tt.want {
				t.Fatalf("captureToAttr(%q) = %q, want %q", tt.capture, got, tt.want)
			}
		})
	}
}
