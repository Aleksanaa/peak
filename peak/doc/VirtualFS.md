# Peak Virtual Filesystem

Peak exposes its internal state as a virtual filesystem (VFS) rooted at
/peak. The VFS is served over a 9P socket at ~/.peak/9p, which makes it
accessible both inside Peak (by typing /peak/... paths) and from external
tools over the socket.


## Accessing via 9P

To access the VFS from outside Peak, mount the socket:

Using 9pfuse:

    9 9pfuse unix!$HOME/.peak/9p <mountpoint>

Using Linux kernel 9P:

    mount -t 9p ~/.peak/9p <mountpoint> -o trans=unix,uname=$USER

Note: when inspecting the VFS from a shell, use a plain shell (e.g. sh)
rather than a configured one. Configured shells may probe the working
directory on startup, which can trigger unwanted behavior inside paths
like /peak/ssh.


## Control Files

These files live directly under /peak:

- event             Global event stream. Each line is "new <id>" or
                    "close <id>" when a window opens or closes.
                    Reads block until an event arrives.
- index             Snapshot of all open windows. Each line has the format:
                    <id> <taglen> <bodylen> <isdir> <isdirty> <tag>
                    Fields are right-aligned in 11-character columns, matching
                    acme's /acme/index format.
- env               Global environment overlay handed to commands and
                    terminals peak starts after it is set. Read returns
                    sorted "KEY=VALUE" lines. Writing merges lines on
                    close; a line with no "=" removes that key. Entries
                    override peak's own environment.
- exec              Write a window title to create an externally-driven
                    terminal window; read back the window ID.
- mount             Write "<socket> <path>" to mount a 9P server at
                    path. Read to list current mounts.
- unmount           Write a path to detach it from the VFS.
- bind              Write "<src> <dst>" to overlay src onto dst. Read
                    to list current binds.
- new/              Walking into this directory creates a new empty
                    window and redirects to its /peak/<id>/ directory.
- srv/              Virtual socket registry. Open read-write to post a
                    service; open read-only to connect to one.


## Per-Window Files

Each open window is accessible at /peak/<id>/:

- body              Window body text. Readable and writable.
- tag               Window tag text. Readable and writable.
- ctl               Control file. Read returns the window status:
                    <id> <taglen> <bodylen> <isdir> <isdirty> <width>
                    terminal <maxtab>. Write executes a command.
- event             Window event stream for externally-driven windows.
                    Reads block until an event arrives.
- addr              Current address as #q0,#q1 character offsets.
                    Readable and writable.
- data              Text within the current address range. Readable and
                    writable. Writing replaces the addressed range.
- rdsel             Read-only snapshot of the selection at open time.
- wrsel             Write-only. Replaces the open-time selection on close.
- errors            Write-only. Appends text to the window error output.
- color             Write-only. Sets the window handle color.
- io                PTY I/O for terminal windows only.


## Built-in Paths

- /peak/doc/        Peak's embedded documentation files.
- /peak/theme/      Color themes. Read to list; write a name to apply
                    (via the Theme command).
- /peak/mirage/     In-memory scratch space. Contents lost on exit.


## SSH (peak-ssh)

SSH filesystem support is provided by the external peak-ssh program.
Run it to mount remote hosts into the VFS:

    peak-ssh

By default it mounts at /peak/ssh. Files are accessed as:

    /peak/ssh/[user@]host[::port]/path/to/file

If no user is given, the current username is used. Authentication uses
SSH_AUTH_SOCK. Use ~ in the path for the remote home directory
(e.g. /peak/ssh/host/~/.bashrc).

Use host::port to specify a non-standard port. A single colon is
reserved for line and column numbers in the plumb syntax.

Commands executed from a window inside /peak/ssh/... run on the remote
host.


## Git (peak-git)

Git repository access is provided by the external peak-git program. Run
it alongside Peak:

    peak-git

peak-git watches window lifecycle events. When a window opens a file
inside a git repository, it mounts that repository's VFS at
<repo>/.git/fs/. The mount is removed when the last window from that
repository is closed.

Each mounted repository exposes:

- HEAD              Current HEAD ref.
- log               Commit log for HEAD.
- status            Working tree status.
- diff              Diff of the working tree against HEAD.
- staged            Staged changes. Readable and writable.
- commit            Write-only. Commits staged changes.
- reset             Write-only. Resets staged changes.
- heads/<branch>/   Local branch directory.
- remotes/<r>/<b>/  Remote branch directory.

Each local branch directory (heads/<branch>/) exposes:

  - log             Commit log.
  - diff            Diff against HEAD.
  - <path>          Files in the branch tree.

Each remote branch directory (remotes/<r>/<b>/) exposes:

  - log             Commit log.
  - <path>          Files in the remote branch tree.
