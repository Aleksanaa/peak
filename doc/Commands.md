# Peak Commands Reference

Peak commands can be typed in any tag or in the text body and executed by Middle-Clicking (Button 2). Every piece of text is a potential command.

## 1. Global Commands

- NewCol: Creates a new vertical column.
- Exit: Quits the editor. If any windows have unsaved changes, it will warn you first.

## 2. Column Commands

- New [filename]: Creates a new empty window or opens the specified file in this column.
- Zerox: Replicates the currently focused window in this column.
- Delcol: Deletes the current column and all its windows. Warns if there are unsaved changes.
- Sort: Sorts all windows in the current column alphabetically by their filename.

## 3. Window Commands

- Get [filename]: 
  - If no filename is given, reloads the current file from disk.
  - If a filename is given, loads that file into the window.
- Put [filename]:
  - If no filename is given, saves the current window buffer to its file.
  - If a filename is given, saves the buffer as that filename (Save As).
- Edit <structural-regex>: Runs a structural regular expression on the current window. (See StructuralRegex.md )
- Undo: Reverts the last text modification.
- Redo: Re-applies a previously undone modification.
- Snarf: Copies the current selection to the system clipboard.
- Cut: Copies the current selection to the clipboard and deletes it.
- Paste: Replaces the current selection (or inserts at cursor) with the clipboard content.
- Zerox: Creates a clone of the current window.
- Del: Closes the current window. Warns if there are unsaved changes.
- Delete: Closes the current window immediately, without checking for unsaved changes.
- Look [pattern]: 
  - Searches for pattern in the window's body.
  - If no pattern is given, searches for the current selection.
- Tab [n]: 
  - If n is provided, sets the tab width for this window to n spaces.
  - If no argument is provided, shows the current tab width in +Errors.

## 4. Special Commands

- Plumb [path/pattern] (Right-Click): 
  - If it's a file path, opens the file.
  - If it's a compiler error (path:line), opens the file at that line.
  - If it's a URL, opens it in the browser.
  - If it's just text, it executes Look on it.

## 5. File Naming Conventions

- ./path: Paths are relative to the directory of the current window.
- ~: Paths starting with ~ are relative to the home directory.
- +Errors: A special window name for command output and error messages.

## 6. Shell Commands

Any command that is not built-in will be executed in a shell (sh -c).
- Example: ls -l will run and show the output in a +Errors window.
- Example: go build will build your project.
- You can select any text (like a shell script or a single command) and middle-click it to run.

## 7. Arguments and Selections

Most commands look for arguments in this order:
1. Text following the command name (e.g., Get main.go).
2. Current text selection in the focused window.
3. The filename in the window's tag (for Get and Put).
