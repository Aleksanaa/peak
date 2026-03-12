# Peak Virtual Filesystem

Peak has a unified internal representation of a simplified virtual filesystem (VFS). This allows Peak to treat local files, remote files, and internal state through a consistent interface.

The filesystem can be accessed in two ways:

1. Inside Peak: By typing /peak in any tag or body.
2. Outside Peak: By mounting it as a 9P filesystem.

## Mounting via 9P

Peak starts a 9P server listening on a Unix socket at ~/.peak/9p. You can mount this on your host system.

### Using 9pfuse (Recommended):

    $ 9 9pfuse unix!'$HOME/.peak/9p' <mountpoint>

Note: You must expand $HOME and use single quotes if you are using bash.

### Using Linux Kernel 9P Support:

    # mount -t 9p ~/.peak/9p <mountpoint> -o trans=unix,uname=$USER

Note: 9pfuse is recommended because Peak cannot unmount itself; you must manually clear the mountpoint.

## Acme Compatibility

The Peak VFS is designed to be a functional superset of the Acme 9P interface. However, this implementation is currently in progress. While the structure is in place to support per-window directories (e.g., /peak/1/body, /peak/1/tag), only the index file is currently fully available for session management.

## VFS Structure

The VFS is rooted at /peak and contains several specialized directories:

### /peak/index

A virtual file that provides a machine-readable list of all open windows in the current Peak session. Each line contains:

- Window ID
- Tag length
- Body length
- Directory flag (1 if directory, 0 otherwise)
- Dirty flag (1 if modified, 0 otherwise)
- Tag text

### /peak/doc

Provides access to Peak's built-in documentation. These files are embedded directly into the Peak binary.

### /peak/ssh

Allows transparent access to remote filesystems via SFTP. Paths follow the format:

    /peak/ssh/user@host/path/to/file

When you access a path under /peak/ssh, Peak automatically:

1. Parses the connection string (user@host).
2. Establishes an SSH connection (using your SSH agent for authentication).
3. Maps file operations to SFTP commands.
4. Supports remote command execution.
