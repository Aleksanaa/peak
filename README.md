# Peak

<img width="2422" height="1517" alt="image" src="https://github.com/user-attachments/assets/ff8f6813-bd30-43bd-b1dd-aaba209870cc" />

Peak is a terminal text editor, modeled on Plan 9's acme. Middle click to run. Right click to plumb (open or find).

## Use of AI

Peak is written with AI assistance. If you consider any AI-assisted code to be slop, I'm sorry that this project is not for you.

I do review the changes and throw away a lot of them.

It's also not just letting the agent copy acme or edwood to TUI. I built it from my experience with acme while adding some nice-to-have features along the way.

Some parts are borrowed intentionally:

- `Edit` (sam's command language) is ported from Edwood's port of acme. Check `peak/sregx.go`.
- `peak/term` is from micro-editor/terminal.
- `peak/regexp` is Edwood's regexp engine, which is itself a modified copy of Go's.
- `internal/vfs/afero` is a trimmed fork of afero, dropping HTTP parts.

## Features

- Mouse chording (Doesn't work for ghostty linux, GTK issue)
- Handles colored by window state
- True SREGX (`Edit`), same as acme's
- `Win` opens terminal window, also support remote ones
- `/peak` VFS, 9P file server for editor state
- Mount and bind filesystems: `Mount`, `Bind`, `Umount`
- External syntax coloring and tree-sitter highlighting (`peak-lsp`)
- Remote files, commands, terminal over SSH (`peak-ssh`)
- Git as `.git/fs` filesystem (`peak-git`)
- 40 themes, including plain old acme style
- No CGO. Pure Go, main executable only 4M in size

## Building

```bash
CGO_ENABLED=0 go build -o bin/ ./peak ./peak-ssh ./peak-git ./peak-lsp
./bin/peak
```

Or with Nix: `nix build`.

macOS has no middle-click by default. To use this editor you need [MiddleClick](https://github.com/artginzburg/MiddleClick).

## Documentation

Check [`peak/doc`](peak/doc). In editor they are at `/peak/doc`. Run `Help` to open the path.

- [Basics](peak/doc/Basics.md): layout, mouse, terminal windows
- [Commands](peak/doc/Commands.md)
- [Shortcuts](peak/doc/Shortcuts.md)
- [Structural regular expressions](peak/doc/StructuralRegex.md)
- [Virtual filesystem](peak/doc/VirtualFS.md): the 9P interface,
  `peak-ssh` and `peak-git`
