package vfs

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"path"
	"sync"

	"github.com/aleksana/peak/internal/vfs/afero"
	"github.com/knusbaum/go9p"
	"github.com/knusbaum/go9p/proto"
)

// Qid type constants
const (
	QTDIR uint8 = 0x80
)

// twriteOverhead is the fixed header cost of a Twrite message on the wire:
// size[4] type[1] tag[2] fid[4] offset[8] count[4].
const twriteOverhead = 4 + 1 + 2 + 4 + 8 + 4

// iounit is the maximum data payload per read/write operation, chosen so that
// a fully-packed Twrite never exceeds proto.MaxMsgLen.
const iounit uint32 = proto.MaxMsgLen - twriteOverhead

// NinePSrv implements the go9p.Srv interface to expose an afero.Fs.
type NinePSrv struct {
	fs afero.Fs
}

func NewNinePSrv(fs afero.Fs) *NinePSrv {
	return &NinePSrv{fs: fs}
}

// ConnCleaner is implemented by NinePConn. Files opened over 9P can call
// RegisterCleanup to schedule work (e.g. unmounting) when the connection drops.
type ConnCleaner interface {
	RegisterCleanup(func())
}

// connAwareFile may be implemented by virtual files that need to know which
// connection opened them so they can register per-connection cleanup hooks.
type connAwareFile interface {
	SetConn(ConnCleaner)
}

type NinePConn struct {
	srv       *NinePSrv
	fids      map[uint32]string
	openFiles map[uint32]afero.File
	mu        sync.Mutex
	cleanups  []func()
}

func (c *NinePConn) RegisterCleanup(f func()) {
	c.mu.Lock()
	c.cleanups = append(c.cleanups, f)
	c.mu.Unlock()
}

func (s *NinePSrv) NewConn() go9p.Conn {
	return &NinePConn{
		srv:       s,
		fids:      make(map[uint32]string),
		openFiles: make(map[uint32]afero.File),
	}
}

func (c *NinePConn) TagContext(tag uint16) context.Context { return context.Background() }
func (c *NinePConn) DropContext(tag uint16)                {}

func (s *NinePSrv) Version(c go9p.Conn, r *proto.TRVersion) (proto.FCall, error) {
	// go9p's ParseCall rejects messages > MaxMsgLen (65535). An Rread carries
	// 11 bytes of header, so max safe data per read is MaxMsgLen-11 = 65524.
	// Capping msize here ensures the client never requests more than that.
	msize := r.Msize
	if msize > proto.MaxMsgLen {
		msize = proto.MaxMsgLen
	}
	return &proto.TRVersion{Header: proto.Header{Type: proto.Rversion, Tag: r.Tag}, Msize: msize, Version: "9P2000"}, nil
}

func (s *NinePSrv) Auth(c go9p.Conn, r *proto.TAuth) (proto.FCall, error) {
	return nil, fmt.Errorf("auth not supported")
}

func (s *NinePSrv) Attach(c go9p.Conn, r *proto.TAttach) (proto.FCall, error) {
	conn := c.(*NinePConn)

	fi, err := s.fs.Stat("/")
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	conn.mu.Lock()
	conn.fids[r.Fid] = "/"
	conn.mu.Unlock()

	return &proto.RAttach{
		Header: proto.Header{Type: proto.Rattach, Tag: r.Tag},
		Qid:    toQid("/", fi),
	}, nil
}

// WalkRedirector may be implemented by an afero.Fs to redirect certain walk
// steps to a different path. The server calls WalkRedirect for each name before
// falling back to the normal Stat lookup. This is the mechanism used for paths
// like "new" that create a resource on access and redirect the fid to it.
type WalkRedirector interface {
	WalkRedirect(dir, name string) (redirectPath string, fi os.FileInfo, ok bool)
}

func (s *NinePSrv) Walk(c go9p.Conn, r *proto.TWalk) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	currentPath, ok := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	wr, _ := s.fs.(WalkRedirector)
	qids := make([]proto.Qid, 0)
	for _, name := range r.Wname {
		var nextPath string
		var fi os.FileInfo

		if wr != nil {
			if rp, rfi, redirected := wr.WalkRedirect(currentPath, name); redirected {
				nextPath, fi = rp, rfi
			}
		}
		if nextPath == "" {
			nextPath = path.Join(currentPath, name)
			var err error
			fi, err = s.fs.Stat(nextPath)
			if err != nil {
				if len(qids) == 0 {
					return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
				}
				return &proto.RWalk{Header: proto.Header{Type: proto.Rwalk, Tag: r.Tag}, Nwqid: uint16(len(qids)), Wqid: qids}, nil
			}
		}

		qids = append(qids, toQid(nextPath, fi))
		currentPath = nextPath
	}

	conn.mu.Lock()
	conn.fids[r.Newfid] = currentPath
	conn.mu.Unlock()
	return &proto.RWalk{Header: proto.Header{Type: proto.Rwalk, Tag: r.Tag}, Nwqid: uint16(len(qids)), Wqid: qids}, nil
}

func (s *NinePSrv) Open(c go9p.Conn, r *proto.TOpen) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	p, ok := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	var flag int
	switch r.Mode & 3 {
	case proto.Oread:
		flag = os.O_RDONLY
	case proto.Owrite:
		flag = os.O_WRONLY
	case proto.Ordwr:
		flag = os.O_RDWR
	case proto.Oexec:
		flag = os.O_RDONLY
	}
	if r.Mode&proto.Otrunc != 0 {
		flag |= os.O_TRUNC
	}

	f, err := s.fs.OpenFile(p, flag, 0)
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	if caf, ok := f.(connAwareFile); ok {
		caf.SetConn(conn)
	}

	fi, _ := f.Stat()

	conn.mu.Lock()
	conn.openFiles[r.Fid] = f
	conn.mu.Unlock()

	return &proto.ROpen{
		Header: proto.Header{Type: proto.Ropen, Tag: r.Tag},
		Qid:    toQid(p, fi),
		Iounit: iounit,
	}, nil
}

func (s *NinePSrv) Create(c go9p.Conn, r *proto.TCreate) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	basePath, ok := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	newPath := path.Join(basePath, r.Name)
	var f afero.File
	var err error

	if r.Perm&0x80000000 != 0 { // DMDIR
		err = s.fs.Mkdir(newPath, os.FileMode(r.Perm&0777))
		if err == nil {
			f, err = s.fs.Open(newPath)
		}
	} else {
		f, err = s.fs.OpenFile(newPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(r.Perm&0777))
	}

	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	fi, _ := f.Stat()

	conn.mu.Lock()
	conn.fids[r.Fid] = newPath
	conn.openFiles[r.Fid] = f
	conn.mu.Unlock()

	return &proto.RCreate{
		Header: proto.Header{Type: proto.Rcreate, Tag: r.Tag},
		Qid:    toQid(newPath, fi),
		Iounit: iounit,
	}, nil
}

func (s *NinePSrv) Read(c go9p.Conn, r *proto.TRead) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	f, ok := conn.openFiles[r.Fid]
	p := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "file not open"}, nil
	}

	fi, err := f.Stat()
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	if fi.IsDir() {
		infos, err := afero.ReadDir(s.fs, p)
		if err != nil {
			return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
		}

		var b []byte
		for _, info := range infos {
			st := toStat(path.Join(p, info.Name()), info)
			b = append(b, st.Compose()...)
		}

		if r.Offset >= uint64(len(b)) {
			return &proto.RRead{Header: proto.Header{Type: proto.Rread, Tag: r.Tag}, Count: 0, Data: nil}, nil
		}

		end := r.Offset + uint64(r.Count)
		if end > uint64(len(b)) {
			end = uint64(len(b))
		}
		data := b[r.Offset:end]
		return &proto.RRead{Header: proto.Header{Type: proto.Rread, Tag: r.Tag}, Count: uint32(len(data)), Data: data}, nil
	}

	data := make([]byte, r.Count)
	n, err := f.ReadAt(data, int64(r.Offset))
	if err != nil && err != io.EOF {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	return &proto.RRead{
		Header: proto.Header{Type: proto.Rread, Tag: r.Tag},
		Count:  uint32(n),
		Data:   data[:n],
	}, nil
}

func (s *NinePSrv) Write(c go9p.Conn, r *proto.TWrite) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	f, ok := conn.openFiles[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "file not open"}, nil
	}

	n, err := f.WriteAt(r.Data, int64(r.Offset))
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	return &proto.RWrite{
		Header: proto.Header{Type: proto.Rwrite, Tag: r.Tag},
		Count:  uint32(n),
	}, nil
}

func (s *NinePSrv) Clunk(c go9p.Conn, r *proto.TClunk) (proto.FCall, error) {
	conn := c.(*NinePConn)

	conn.mu.Lock()
	f, hasFile := conn.openFiles[r.Fid]
	if hasFile {
		delete(conn.openFiles, r.Fid)
	}
	delete(conn.fids, r.Fid)
	conn.mu.Unlock()

	if hasFile {
		f.Close()
	}

	return &proto.RClunk{Header: proto.Header{Type: proto.Rclunk, Tag: r.Tag}}, nil
}

func (s *NinePSrv) Remove(c go9p.Conn, r *proto.TRemove) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	p, ok := conn.fids[r.Fid]
	f, hasFile := conn.openFiles[r.Fid]
	if hasFile {
		delete(conn.openFiles, r.Fid)
	}
	delete(conn.fids, r.Fid)
	conn.mu.Unlock()

	if hasFile {
		f.Close()
	}

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	err := s.fs.Remove(p)
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	return &proto.RRemove{Header: proto.Header{Type: proto.Rremove, Tag: r.Tag}}, nil
}

func (s *NinePSrv) Stat(c go9p.Conn, r *proto.TStat) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	p, ok := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	fi, err := s.fs.Stat(p)
	if err != nil {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
	}

	return &proto.RStat{
		Header: proto.Header{Type: proto.Rstat, Tag: r.Tag},
		Stat:   toStat(p, fi),
	}, nil
}

func (s *NinePSrv) Wstat(c go9p.Conn, r *proto.TWstat) (proto.FCall, error) {
	conn := c.(*NinePConn)
	conn.mu.Lock()
	p, ok := conn.fids[r.Fid]
	conn.mu.Unlock()

	if !ok {
		return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: "unknown fid"}, nil
	}

	// Rename check
	if r.Stat.Name != "" && r.Stat.Name != path.Base(p) {
		newPath := path.Join(path.Dir(p), r.Stat.Name)
		err := s.fs.Rename(p, newPath)
		if err != nil {
			return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
		}
		conn.mu.Lock()
		conn.fids[r.Fid] = newPath
		conn.mu.Unlock()
		p = newPath
	}

	// Length (Truncate) check
	if r.Stat.Length != 0xFFFFFFFFFFFFFFFF {
		f, err := s.fs.OpenFile(p, os.O_WRONLY, 0)
		if err == nil {
			err = f.Truncate(int64(r.Stat.Length))
			f.Close()
		}
		if err != nil {
			return &proto.RError{Header: proto.Header{Type: proto.Rerror, Tag: r.Tag}, Ename: err.Error()}, nil
		}
	}

	return &proto.RWstat{Header: proto.Header{Type: proto.Rwstat, Tag: r.Tag}}, nil
}

// Accepter is implemented by virtual listen sockets (e.g. srvServerFile) that
// deliver independent per-connection transports, one per dial.
type Accepter interface {
	Accept() (io.ReadWriteCloser, error)
	Close() error
}

// ServeAccepter runs the accept loop for a virtual listen socket, serving each
// incoming connection as an independent 9P session in its own goroutine.
func (s *NinePSrv) ServeAccepter(a Accepter) {
	for {
		rwc, err := a.Accept()
		if err != nil {
			return
		}
		go s.ServeConn(rwc)
	}
}

// ServeConn serves a single pre-established 9P connection over rwc.
func (s *NinePSrv) ServeConn(rwc io.ReadWriteCloser) {
	defer rwc.Close()
	conn := &NinePConn{
		srv:       s,
		fids:      make(map[uint32]string),
		openFiles: make(map[uint32]afero.File),
	}
	cs := &connSrv{NinePSrv: s, conn: conn}
	pr, pw := io.Pipe()
	go func() {
		io.Copy(pw, rwc)
		conn.cleanup()
		pw.Close()
	}()
	go9p.ServeReadWriter(bufio.NewReader(pr), rwc, cs)
	pr.Close()
}

func (s *NinePSrv) Serve(network, address string) error {
	l, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	s.ServeListener(l)
	return nil
}

// connSrv wraps NinePSrv so that ServeListener can supply the NinePConn
// that go9p creates internally via NewConn.
type connSrv struct {
	*NinePSrv
	conn *NinePConn
}

func (cs *connSrv) NewConn() go9p.Conn { return cs.conn }

func (s *NinePSrv) ServeListener(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()

			conn := &NinePConn{
				srv:       s,
				fids:      make(map[uint32]string),
				openFiles: make(map[uint32]afero.File),
			}
			cs := &connSrv{NinePSrv: s, conn: conn}

			// Pipe the connection through so we get a hook when the peer
			// drops. go9p.ServeReadWriter dispatches 9P calls in worker
			// goroutines and only returns after all workers exit. One of
			// those workers can be permanently blocked in a blocking Read
			// (e.g. the window event file). conn.cleanup() unblocks it by
			// closing the subscription, but it can only be called once the
			// connection is known to be dead — not after the workers finish.
			//
			// Solution: copy the raw bytes through a pipe. When the real
			// connection drops, io.Copy returns, we call conn.cleanup()
			// (which unblocks the stuck worker), then close the write end of
			// the pipe so that ServeReadWriter sees EOF and the workers drain.
			pr, pw := io.Pipe()
			go func() {
				io.Copy(pw, c)
				conn.cleanup()
				pw.Close()
			}()

			go9p.ServeReadWriter(bufio.NewReader(pr), c, cs)
			pr.Close()
		}(c)
	}
}

// cleanup closes all files remaining open when a client drops without sending
// Clunk (e.g. process killed). This releases event subscriptions so that any
// worker goroutine blocked on a blocking Read is unblocked and can exit. It
// also runs any cleanup hooks registered via RegisterCleanup (e.g. unmounting
// filesystems that were mounted over this connection).
func (c *NinePConn) cleanup() {
	c.mu.Lock()
	files := make([]afero.File, 0, len(c.openFiles))
	for _, f := range c.openFiles {
		files = append(files, f)
	}
	c.openFiles = make(map[uint32]afero.File)
	cleanups := c.cleanups
	c.cleanups = nil
	c.mu.Unlock()
	for _, f := range files {
		f.Close()
	}
	for _, fn := range cleanups {
		fn()
	}
}

func toQid(p string, fi os.FileInfo) proto.Qid {
	var q proto.Qid
	if fi.IsDir() {
		q.Qtype = QTDIR
	}
	h := fnv.New64a()
	h.Write([]byte(p))
	q.Uid = h.Sum64()
	return q
}

func toStat(p string, fi os.FileInfo) proto.Stat {
	st := proto.Stat{
		Name:   fi.Name(),
		Uid:    "guest",
		Gid:    "guest",
		Muid:   "guest",
		Length: uint64(fi.Size()),
		Mtime:  uint32(fi.ModTime().Unix()),
		Atime:  uint32(fi.ModTime().Unix()),
	}
	if fi.IsDir() {
		st.Mode = 0x80000000 | uint32(fi.Mode()&0777)
	} else {
		st.Mode = uint32(fi.Mode() & 0777)
	}
	st.Qid = toQid(p, fi)
	return st
}
