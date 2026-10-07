# Structural Regular Expressions

Peak's Edit command runs sam's command language as acme does; the code is
ported from Edwood's and checked against acme's. Unlike line-oriented
tools like sed, it operates on arbitrary ranges of text and composes
commands recursively.


## 1. Using Edit

Type a command in any tag or body and middle-click it:

    Edit <command>

Edit operates on dot, the current selection, unless the command gives an
address. The , address (whole file) is commonly used explicitly:

    Edit , <command>

The changes all commands make are applied together when they have run,
so every address refers to the text as it was, and one Undo reverts them.
If a command fails, none are applied. What commands print, and why one
failed, goes to +Errors.


## 2. Addresses

Addresses specify which part of the text a command operates on.

- .                 Dot.
- 0                 The beginning of the file.
- $                 The end of the file.
- #n                Character offset n.
- n                 Line n, with its newline.
- /re/              The next match of regular expression re, wrapping
                    around the end of the file.
- ?re?              The previous match of re, wrapping around.
- "re"              Dot in the one file whose line in the file list
                    (see b) re matches.
- a+n, a-n          n lines (default 1) after or before a.
- a1,a2             From the start of a1 to the end of a2. A missing a1
                    is 0, a missing a2 is $.
- a1;a2             Like a1,a2, but evaluates a2 with dot set to a1.

An empty pattern // reuses the last regular expression.


## 3. Basic Commands

- a/text/           Append text after the addressed range.
- i/text/           Insert text before the addressed range.
- c/text/           Replace the addressed range with text.
- d                 Delete the addressed range.
- p                 Print the addressed range.
- s/re/text/        Substitute the first match of re with text.
- s/re/text/g       Substitute all matches of re with text.
- sn/re/text/       Substitute the nth match of re (n >= 1).
- w [file]          Write the range (default: the whole file) to file, or
                    to the window's file.
- e [file]          Replace the text with the contents of file, or of the
                    window's file, and name the window file. Warns once
                    about unsaved changes.
- r [file]          Replace the addressed range with the contents of file,
                    or of the window's file.
- m addr            Move the addressed range to after addr.
- t addr            Copy the addressed range to after addr.
- u [n]             Undo the last n changes (default 1). u-n redoes them.
- f [file]          Name the window file, and print its line in the file
                    list.
- =                 Print the line numbers of the addressed range.
- =#                Print its character offsets.
- =+                Print its line and character offsets.
- (newline)         With an address, select it and show it; without one,
                    select dot's lines, or the next line if they are
                    selected already.

In substitution text, & expands to the whole match, \1 through \9 to
the corresponding subgroups, and \n to a newline.

For a, i, and c, text can also be given as a multi-line block: omit the
delimiter and start the text on the next line, terminated by a line
containing only a dot:

    a
    first line
    second line
    .


## 4. Structural Commands

- x/re/ cmd         For each match of re in the range, set dot to it and
                    run cmd. Defaults to p if no command is given.
- x cmd             For each line in the range, run cmd.
- y/re/ cmd         Run cmd on each region between matches of re.
- g/re/ cmd         If the range contains a match of re, run cmd.
                    Defaults to p.
- v/re/ cmd         If the range contains no match of re, run cmd.
                    Defaults to p.
- { ... }           Group commands, one per line; each starts at the
                    same dot.


## 5. Multi-file Commands

Each window holding a file has a line in the file list: a ' if it has
unsaved changes, a +, a . if it is the current window, a space, and its
name, as in

     +. /home/user/main.go

- X/re/ cmd         Run cmd in each window whose line re matches.
                    Defaults to f if no command is given.
- X cmd             Run cmd in every window with a name.
- Y/re/ cmd         Run cmd in each window whose line re does not match.
- B file ...        Open files. With <cmd, the files cmd prints.
- D [file ...]      Close the files, or the current window. Warns once
                    about unsaved changes.
- b file            Make the window holding file current, and print its
                    line.

Directories, +Errors and terminals hold no file for b and "; X and Y pass
terminals by. A terminal's text can be printed and addressed but not
changed.


## 6. Shell Commands

Commands run in the window's directory.

- |cmd              Pipe the range through cmd and replace it with the output.
- >cmd              Pipe the range to cmd, and print what it prints.
- <cmd              Replace the range with the output of cmd.


## 7. Examples

Delete all trailing whitespace:

    Edit , x/[ \t]+\n/ s/[ \t]+\n/\n/

Comment out every line containing "TODO":

    Edit , x/.*TODO.*\n/ i/\/\/ /

Rename a field across all Go files:

    Edit X/\.go$/ , x/OldName/ c/NewName/

Print the line number of every match of "error":

    Edit , x/error/ =

Uppercase all occurrences of "peak":

    Edit , x/peak/ |tr a-z A-Z

Wrap each match of a pattern in parentheses using a back-reference:

    Edit , s/foo(\w+)/(\1)/g
