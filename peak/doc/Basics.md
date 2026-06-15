# Peak Basics

Peak is a TUI text editor inspired by Plan 9 Acme. Text is the primary
interface: you write what you want to do and execute it.


## 1. The Layout

The editor is divided into several areas, all editable:

- Global Tag (top row): Contains global commands NewCol Help Exit.
  You can type any command here and execute it.
- Columns: The screen is divided vertically into columns. Each column
  has a Column Tag at the top with commands New Zerox Win Delcol.
- Windows: Each column has one or more windows, each with a Window Tag
  and a Body.

The Window Tag shows the filename and Get Put Undo Redo Snarf Zerox Del
by default, but it is a plain text buffer and you can type or execute
anything there.

The Handle is the small colored area at the left edge of each column and
window. Drag it to move or resize elements.


## 2. Mouse Interaction

Three mouse buttons do different things:

- Button 1 (Left):
  - Click to focus a window or tag.
  - Drag to select text.
  - Drag a Handle to move or resize columns and windows.

- Button 2 (Right): Plumb.
  - Sends the clicked text to the plumber, which decides what to do with it.
  - If the text is a file path, Peak opens it.
  - Supports line and column navigation:
    - path           opens the file.
    - path:line      opens the file at the given line.
    - path:line:col  opens the file at the given line and column.
  - SSH paths with ports use host::port to avoid ambiguity with line numbers.
  - URLs (http://, https://, mailto:, magnet:) are opened in the system browser.
  - If the text is not a recognized path or URL, Peak searches for it (Look).

- Button 3 (Middle): Execute.
  - Middle-clicking a word executes it as a command.
  - Selecting text first and then middle-clicking executes the whole selection.
  - Commands can be built-ins (Put, Get) or any shell command (ls, make, ...).
  - You can execute text from anywhere: the tag, the body, or command output.

### Mouse Chording

While holding Button 1 (Left) in a window body or window tag, pressing a
second button triggers an editing chord:

- Button 1 + Button 3 (Middle): Cut the active selection. If no non-empty
  selection is active, nothing happens.
- Button 1 + Button 2 (Right): Paste from the clipboard at the cursor, or
  replace the active selection if one exists.

Chords are not active in terminal windows, the global tag, column tags,
scroll gutters, or resize handles.


### Scrollbar

The thin vertical bar on the left of a window body is the scrollbar.

- Left               Scroll up.
- Right              Scroll down.
- Middle             Jump to that position in the file.


## 3. Commands

Middle-click any word to execute it as a command. Built-ins cover window
and file management (Get, Put, New, Win, Del, Zerox, ...), text operations
(Undo, Redo, Snarf, Look, Edit), and more. Any non-built-in word runs as
a shell command; output goes to +Errors. Pipe operators (<, >, |) work on
the current selection.

See Commands.md for the full reference.


## 4. Terminal Windows

Win opens a terminal window running your shell in the current directory.
Input goes directly to the shell, so text editing commands (Get, Put,
Undo, Redo, and text modification part in Edit) do not apply. Keyboard
shortcuts that would conflict with shell input require the Alt modifier:

- Alt+Ctrl-C        Copy to clipboard.
- Alt+Ctrl-V        Paste from clipboard.
- Alt+Ctrl-F        Look on the current selection.
- Alt+PgUp          Scroll up.
- Alt+PgDn          Scroll down.
- Ctrl+Middle-click Execute a Peak command.
- Ctrl+Right-click  Plumb.

### Path Following

Right-clicking a path or running a command uses the terminal's current
directory. To enable this, the shell must emit OSC 7 on each prompt:

```
\e]7;file://hostname/path\a
```

Fish shell supports this out of the box. For other shells, add the escape
to the respective prompt function or hook.


## 5. Keyboard Shortcuts

- Ctrl-C            Copy to clipboard.
- Ctrl-X            Cut to clipboard.
- Ctrl-V            Paste from clipboard.
- Ctrl-Z / Ctrl-Y   Undo / Redo.
- Ctrl-F            Run Look on the current selection.

See Shortcuts.md for the full list of navigation and editing shortcuts.


## 6. Typical Workflow

1. Open a file: right-click its path anywhere in the editor.
2. Edit: click in the body and type.
3. Save: middle-click Put in the window tag.
4. Search: select a word and middle-click Look, or press Ctrl-F.
5. Run a command: type ls -l anywhere, select it, and middle-click it.
