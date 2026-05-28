# tview

A minimal, stripped-down layout rendering library extracted from
[rivo/tview](https://github.com/rivo/tview).

## What's kept

- `Primitive` — interface with `Draw`, `GetRect`, `SetRect`
- `Box` — background fill, border, title, padding
- `Flex` — row/column container with fixed-size + proportional distribution
- `Print` — text rendering with alignment and truncation
- Border rune constants (box-drawing Unicode)

## What's stripped

- All preset styles and themes (`Styles`, `Theme`)
- Focus system (`Focus`, `Blur`, `HasFocus`, `focusChain`)
- Input/mouse/paste event handling
- All widgets (`Button`, `TextView`, `Table`, etc.)
- `Grid`, `Frame`, `Pages` layout containers
- Style tag parsing (e.g. `[red]text[white]`)
- `Application` event loop

## License

MIT — see [LICENSE](./LICENSE), derived from [rivo/tview](https://github.com/rivo/tview).
