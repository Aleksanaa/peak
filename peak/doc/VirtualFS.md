# Peak Virtual Filesystem

Peak serves its state as files under /peak. Inside Peak they are opened like
any path; outside it, they are served over 9P on the Unix socket
~/.peak/9p.<pid>, which Peak sets in $PEAK for the programs it runs:

    9 9pfuse unix!$PEAK <mountpoint>
    mount -t 9p $PEAK <mountpoint> -o trans=unix,uname=$USER


## Control Files

- event             Window events, a line each: "<kind> <id> <name>",
                    where kind is new, close, focus, get or put, and name
                    is the window's name, the rest of the line, unquoted.
                    Reads block until there is one.
- index             A line for each window, in the format of acme's index:
                    <id> <taglen> <bodylen> <isdir> <isdirty> <tag>,
                    each number 11 characters wide.
- mount             Write "<socket> <path>" to mount a 9P server at path:
                    a service posted in srv/, or one on a Unix socket.
                    Read for the mounts.
- unmount           Write a path to unmount it.
- bind              Write "<src> <dst>" to bind src onto dst. Read for the
                    binds.
- new/              Walking into it makes a new window, as New does, and
                    leads to its directory, /peak/<id>/.
- srv/              Posted 9P services. Open srv/<name> read-write and
                    serve 9P on it to post one; mount it through mount.
                    All mounts of a service share one conversation with
                    it, as on Plan 9.

Paths in mount, unmount and bind are backtick-quoted when they contain
spaces (see Quoting in Commands).


## Window Files

Each window has a directory, /peak/<id>/. What is written to a file takes
effect when it is closed, except for ctl and event.

- body              The body. Writing replaces it; in a terminal, what is
                    written is typed instead.
- tag               The tag. Its first field is the window's name,
                    backtick-quoted if it contains spaces.
- ctl               Reads "<id> <taglen> <bodylen> <isdir> <isdirty>
                    <width> terminal <maxtab>". Each write runs a command,
                    as if executed in the tag.
- event             Reads the window's events as records:
                    <origin><type><q0> <q1> <flag> <nr> <text>: KI and KD
                    for text inserted and deleted, Mx and Ml for
                    text clicked to execute and to plumb.
                    Writing an x or l record back executes or plumbs its
                    text.
- addr              The address, as #q0,#q1. Write #n, #n,#m or a line
                    number to set it.
- data              The text at the address. Writing replaces it, and the
                    address becomes what was written.
- rdsel             The selection when it was opened.
- wrsel             Writing replaces the selection as it was when the file
                    was opened.
- errors            Writing appends to the window's +Errors.
- color             Lines of "<q0> <q1> <attr>" color the body: the range
                    is drawn in the theme's syntax color attr, as keyword
                    for SynKeyword. A write replaces the ranges before it,
                    and an empty one clears them, as does closing the
                    event file of a program reading it.


## Built-in Paths

- /peak/doc/        This documentation.
- /peak/theme/      The color themes, a file each, applied by Theme <name>.
                    Changes to them last until Peak exits.
- /peak/mirage/     Files kept in memory until Peak exits.


## SSH (peak-ssh)

peak-ssh serves remote hosts' files, mounted at /peak/ssh:

    peak-ssh

A file is /peak/ssh/[user@]host[:port]/path; the user defaults to yours
and the port to 22. ~ is the remote home directory, as in
/peak/ssh/host/~/.bashrc. It authenticates through SSH_AUTH_SOCK.

Commands executed in a window under /peak/ssh/ run on the remote host.


## Git (peak-git)

peak-git serves the git repositories of the files Peak has open:

    peak-git

When a window opens a file in a repository, it mounts the repository at
<repo>/.git/fs/, and unmounts it when the repository's last window closes.

- HEAD              The current HEAD.
- log               The log of HEAD.
- status            The working tree's status.
- diff              The working tree's diff against HEAD.
- staged            The staged changes. Writing a list of paths stages
                    them instead.
- commit            Writing a message commits the staged changes; lines
                    starting with # are left out.
- reset             Writing hard or soft resets so; anything else is a
                    mixed reset.
- heads/<branch>/   A branch: its log, its diff against HEAD, and its
                    files.
- remotes/<r>/<b>/  A remote branch: its log and its files.
