# Peak Basics

Peak is a TUI (Terminal User Interface) text editor inspired by the Plan 9 Acme editor. It follows the philosophy that everything is editable text, and every piece of text can be a command.

The core soul of Peak is interaction, where you write what you want to do and execute it.

## 1. The Layout

The editor is divided into several areas, all of which are editable:

- Global Tag (Top Row): Contains global commands like NewCol and Exit. You can type any command here to run it globally.
- Columns: The screen is divided vertically into columns. Each column has its own Column Tag (top of the column) with commands like New and Delcol.
- Windows: Each column contains one or more windows. A window has a Window Tag (top of the window) and a Body (where you edit text).

The Window Tag usually contains the filename and standard commands (Get, Put, Look, etc.), but it is just a text buffer—you can type and execute anything there.

The Handle is the small colored area at the top-left of each column and window. You can click and drag it to move or resize elements.

## 2. Mouse Interaction (The Acme Way)

Acme-style editors rely heavily on three mouse buttons to interact with text as data and commands.

- Button 1 (Left Click): 
  - Click to focus a window or tag.
  - Drag to select text.
  - Drag a Handle to move or resize columns and windows.
- Button 2 (Middle Click): The "Execute" button.
  - Middle-clicking a word executes it as a command.
  - If you select text and middle-click the selection, it executes the entire selection.
  - Commands can be built-ins (like Put) or any external shell command (like ls).
  - IMPORTANT: You can execute text from anywhere—the tag, the body, or even the output of another command.
- Button 3 (Right Click): The "Plumb" button.
  - It "sends" the text to the plumber to decide what to do.
  - If the text is a filename or path that exists, Peak opens it.
  - Supports navigation to specific lines and columns:
    - path           : Opens the file.
    - path:line      : Opens the file and jumps to the specified line.
    - path:line:col  : Opens the file and jumps to the specific line and column.
  - SSH paths with ports can use host::port to avoid ambiguity with line numbers.
  - If it's a plain word, Peak searches for it (Look).

### Scrolling (The Scrollbar Handle)
The thin vertical bar on the left of a window's body is the scrollbar.
- Left Click: Scroll up.
- Right Click: Scroll down.
- Middle Click: Jump to that position in the file.

## 3. Essential Commands

You can execute these by middle-clicking them in any tag or even in the text body:

- Get: (Re)loads the file from disk.
- Put: Saves the current buffer to disk.
- New: Opens a new window.
- Del: Closes the current window.
- Undo / Redo: Standard history navigation.
- Snarf: Copies the current selection to the system clipboard.
- Look: Searches for the selected text (or the word under the cursor).
- Exit: Quits Peak.

External Commands: Any word that isn't a built-in command (like ls, grep, or make) will be executed as a shell command. The output usually appears in a new window named +Errors.

## 4. Keyboard Shortcuts

While the mouse is primary, some standard keyboard shortcuts are available:

- Ctrl-C / Ctrl-X / Ctrl-V: Copy / Cut / Paste.
- Ctrl-Z / Ctrl-Y: Undo / Redo.
- Ctrl-F: Execute Look on the current selection.

See Shortcuts.md for a complete list of navigation and editing shortcuts.

## 5. Typical Workflow

1. Open a file: Type the filename in a tag and Right-Click it.
2. Edit: Click in the body and start typing.
3. Save: Middle-click the Put command in the window's tag.
4. Search: Select a word and Middle-click Look (or press Ctrl-F).
5. Run a command: Type ls -l anywhere, select it, and Middle-click it.
