# Structural Regular Expressions in Peak

Peak's Edit command implements a powerful structural regular expression engine, similar to the one found in the Acme and Sam editors. Unlike traditional line-oriented tools (like sed), structural regex allows you to manipulate the structure of the text itself.

## 1. Using the Edit Command

The Edit command (usually abbreviated to E) takes a structural regex as an argument:

    Edit <regex>

You can execute it by typing it in a tag and middle-clicking it. It operates on the current selection or the entire file if nothing is selected.

## 2. Addresses

Addresses specify which part of the text a command should operate on.

- . : The current selection (dot).
- 0 : The beginning of the file.
- $ : The end of the file.
- #n : Character offset n.
- n : Line n.
- /re/ : The next match of the regular expression re.
- ?re? : The previous match of re.
- a1,a2 : The range from the start of a1 to the end of a2.
- a1;a2 : Like a1,a2, but sets the context to a1 before evaluating a2.

## 3. Basic Commands

- a/text/ : Append text after the addressed range.
- i/text/ : Insert text before the addressed range.
- c/text/ : Change the addressed range to text.
- d : Delete the addressed range.
- p : Print the addressed range (output usually goes to +Errors).
- s/re/text/ : Substitute re with text within the addressed range.
- w [file] : Write the addressed range to file.
- m addr : Move the addressed range to after addr.
- t addr : Copy (transfer) the addressed range to after addr.
- u [n] : Undo the last n modifications (default 1).
- f [file] : Set the filename to file, or print current filename if omitted.
- = : Print the current byte-offset address.

## 4. Structural Commands (The Powerhouses)

These commands allow you to loop over matches, guard execution, or group commands.

- x/re/ command : Extract. For each match of re in the current range, set the selection to that match and run command.
- y/re/ command : Exchange. Like x, but runs command on the text *between* matches of re.
- g/re/ command : Guard. If the current range matches re, run command.
- v/re/ command : Invert Guard. If the current range does *not* match re, run command.
- { command1 \n command2 \n ... } : Group multiple commands to run on the same address.

## 5. Multi-file Commands

- X/re/ command : For every open window whose filename matches re, run command.
- Y/re/ command : For every open window whose filename does *not* match re, run command.
- B [files] : Open the list of files.
- D [files] : Close the list of files (default current file).
- b [win] : Change the context to the window win.

## 6. Shell Integration

- |command : Pipe the addressed range through an external shell command and replace the range with the output.
- >command : Pipe the addressed range to the input of command.
- <command : Replace the addressed range with the output of command.
- !command : Run command in the shell (standard Acme behavior).

## 7. Examples

- Uppercase all occurrences of "peak":

    Edit , x/peak/ |tr a-z A-Z

- Delete all trailing whitespace in the file:

    Edit , x/[ 	]+$/ d

- Comment out every line containing "TODO":

    Edit , x/.*TODO.*/ i/\/\/ /

- Find "func" in all Go files:

    Edit X/\.go$/ , x/func .*/ p

- Complex formatting:

    Edit , x/struct \{[^}]+\}/ g/FieldName/ s/FieldName/NewName/
