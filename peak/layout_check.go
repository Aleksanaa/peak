package main

import "github.com/aleksana/peak/peak/tview"

var (
	_ tview.Primitive = (*TextView)(nil)
	_ tview.Primitive = (*TermView)(nil)
	_ tview.Primitive = (*Window)(nil)
	_ tview.Primitive = (*Column)(nil)
	_ tview.Primitive = (*bodyWithGutter)(nil)
)
