package vfs

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/knusbaum/go9p/proto"
)

const (
	muxNoFid uint32 = ^uint32(0) // NOFID
	muxNoTag uint16 = ^uint16(0) // NOTAG
)

// NinePMux multiplexes N 9P client connections onto one server connection,
// remapping fids and tags so the server sees a single conversation. The
// server never knows how many clients are connected.
type NinePMux struct {
	conn io.ReadWriteCloser // server pipe (left end)
	wmu  sync.Mutex         // serialises writes to conn

	mu      sync.Mutex
	clients map[uint64]*muxClient
	nextCID uint64

	nextFid  uint32
	freeFids []uint32

	nextTag  uint16
	freeTags []uint16

	pending map[uint16]*pendingReq // serverTag → req

	msize uint32
	ready chan struct{} // closed after Tversion exchange with server
	done  chan struct{} // closed on server disconnect or Close()
}

type muxClient struct {
	id   uint64
	conn io.ReadWriteCloser
	wmu  sync.Mutex

	fids map[uint32]uint32 // clientFid → serverFid
}

type pendingReq struct {
	client    *muxClient // nil = discard (cleanup-originated requests)
	clientTag uint16
	msgType   uint8
	flushed   bool // discard R-message; late arrival after Tflush

	// serverFid: context-dependent fid for response-time cleanup.
	//   Tattach, Tauth, Twalk (newfid only when newfid≠fid): freed on Rerror
	//   Tclunk, Tremove: freed on any response
	serverFid  uint32
	clientFid  uint32 // client-side fid to remove from c.fids on Rerror
	freeOnErr  bool   // free serverFid + remove clientFid on Rerror
	freeAlways bool   // free serverFid always (Tclunk, Tremove)

	isFlush      bool
	serverOldTag uint16 // for Tflush: server tag of the message being flushed
}

func NewNinePMux(conn io.ReadWriteCloser) *NinePMux {
	return &NinePMux{
		conn:    conn,
		clients: make(map[uint64]*muxClient),
		pending: make(map[uint16]*pendingReq),
		nextFid: 1,
		nextTag: 0,
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// allocTag must be called with m.mu held.
func (m *NinePMux) allocTag() uint16 {
	if len(m.freeTags) > 0 {
		t := m.freeTags[len(m.freeTags)-1]
		m.freeTags = m.freeTags[:len(m.freeTags)-1]
		return t
	}
	for {
		t := m.nextTag
		m.nextTag++
		if m.nextTag == muxNoTag {
			m.nextTag = 0
		}
		if _, inUse := m.pending[t]; !inUse {
			return t
		}
	}
}

// freeTag removes the pending entry and returns the tag to the pool.
// Must be called with m.mu held.
func (m *NinePMux) freeTag(t uint16) {
	delete(m.pending, t)
	m.freeTags = append(m.freeTags, t)
}

// allocFid must be called with m.mu held.
func (m *NinePMux) allocFid() uint32 {
	if len(m.freeFids) > 0 {
		f := m.freeFids[len(m.freeFids)-1]
		m.freeFids = m.freeFids[:len(m.freeFids)-1]
		return f
	}
	f := m.nextFid
	m.nextFid++
	if m.nextFid == muxNoFid {
		m.nextFid = 1
	}
	return f
}

// freeFid must be called with m.mu held.
func (m *NinePMux) freeFid(f uint32) {
	if f != muxNoFid {
		m.freeFids = append(m.freeFids, f)
	}
}

// writeServer serialises and writes a message to the server, patching the
// tag in the composed bytes so the caller does not need to set it on the struct.
func (m *NinePMux) writeServer(fc proto.FCall, serverTag uint16) error {
	data := fc.Compose()
	binary.LittleEndian.PutUint16(data[5:7], serverTag)
	m.wmu.Lock()
	defer m.wmu.Unlock()
	_, err := m.conn.Write(data)
	return err
}

func (c *muxClient) write(fc proto.FCall, clientTag uint16) error {
	data := fc.Compose()
	binary.LittleEndian.PutUint16(data[5:7], clientTag)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(data)
	return err
}

// Serve sends Tversion to the server, waits for Rversion, then enters the
// server-reader loop. Must be called as a goroutine by the first Dial().
func (m *NinePMux) Serve() {
	tv := &proto.TRVersion{
		Header:  proto.Header{Type: proto.Tversion, Tag: muxNoTag},
		Msize:   proto.MaxMsgLen,
		Version: "9P2000",
	}
	closeDone := func() {
		select {
		case <-m.done:
		default:
			close(m.done)
		}
	}
	if err := m.writeServer(tv, muxNoTag); err != nil {
		close(m.ready)
		closeDone()
		return
	}
	fc, err := proto.ParseCall(m.conn)
	if err != nil {
		close(m.ready)
		closeDone()
		return
	}
	rv, ok := fc.(*proto.TRVersion)
	if !ok || rv.Type != proto.Rversion {
		close(m.ready)
		closeDone()
		return
	}
	m.msize = rv.Msize
	close(m.ready)
	m.serverReader()
}

func (m *NinePMux) serverReader() {
	defer func() {
		select {
		case <-m.done:
		default:
			close(m.done)
		}
		m.conn.Close()
		m.mu.Lock()
		for _, c := range m.clients {
			c.conn.Close()
		}
		m.mu.Unlock()
	}()
	for {
		fc, err := proto.ParseCall(m.conn)
		if err != nil {
			return
		}
		m.handleRMessage(fc)
	}
}

func (m *NinePMux) handleRMessage(fc proto.FCall) {
	serverTag := fc.GetTag()

	m.mu.Lock()
	req, ok := m.pending[serverTag]
	if !ok {
		m.mu.Unlock()
		return
	}

	_, isError := fc.(*proto.RError)

	if req.flushed {
		if req.freeAlways || (req.freeOnErr && isError) {
			m.freeFid(req.serverFid)
		}
		m.freeTag(serverTag)
		m.mu.Unlock()
		return
	}

	if req.isFlush {
		if orig, exists := m.pending[req.serverOldTag]; exists {
			orig.flushed = true
		}
		clientTag := req.clientTag
		client := req.client
		m.freeTag(serverTag)
		m.mu.Unlock()
		if client != nil {
			rfl := &proto.RFlush{Header: proto.Header{Type: proto.Rflush, Tag: clientTag}}
			client.write(rfl, clientTag)
		}
		return
	}

	// Fid cleanup
	if req.freeAlways {
		m.freeFid(req.serverFid)
	} else if req.freeOnErr && isError {
		m.freeFid(req.serverFid)
		if req.client != nil {
			delete(req.client.fids, req.clientFid)
		}
	}

	clientTag := req.clientTag
	client := req.client
	m.freeTag(serverTag)
	m.mu.Unlock()

	if client == nil {
		return
	}
	client.write(fc, clientTag)
}

// Dial waits for the mux to be ready, creates a client pipe, and starts a
// per-client goroutine. Returns the client end of the pipe.
func (m *NinePMux) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	select {
	case <-m.ready:
	case <-m.done:
		return nil, fmt.Errorf("9p mux: server disconnected before ready")
	case <-ctx.Done():
		return nil, fmt.Errorf("9p mux: %w", ctx.Err())
	}
	select {
	case <-m.done:
		return nil, fmt.Errorf("9p mux: server disconnected")
	default:
	}

	clientLeft, clientRight := net.Pipe()
	c := &muxClient{
		conn: clientRight,
		fids: make(map[uint32]uint32),
	}
	m.mu.Lock()
	c.id = m.nextCID
	m.nextCID++
	m.clients[c.id] = c
	m.mu.Unlock()

	go c.serve(m)
	return clientLeft, nil
}

func (c *muxClient) serve(m *NinePMux) {
	defer m.cleanupClient(c)
	for {
		fc, err := proto.ParseCall(c.conn)
		if err != nil {
			return
		}
		if err := m.handleTMessage(c, fc); err != nil {
			return
		}
	}
}

func (m *NinePMux) handleTMessage(c *muxClient, fc proto.FCall) error {
	switch msg := fc.(type) {
	case *proto.TRVersion:
		// Tversion: answer locally with the negotiated msize.
		rver := &proto.TRVersion{
			Header:  proto.Header{Type: proto.Rversion, Tag: msg.Tag},
			Msize:   m.msize,
			Version: "9P2000",
		}
		c.write(rver, msg.Tag)
		return nil

	case *proto.TAuth:
		m.mu.Lock()
		serverAfid := m.allocFid()
		c.fids[msg.Afid] = serverAfid
		serverTag := m.allocTag()
		m.pending[serverTag] = &pendingReq{
			client:    c,
			clientTag: msg.Tag,
			msgType:   proto.Tauth,
			serverFid: serverAfid,
			clientFid: msg.Afid,
			freeOnErr: true,
		}
		m.mu.Unlock()
		msg.Afid = serverAfid
		return m.writeServer(msg, serverTag)

	case *proto.TAttach:
		m.mu.Lock()
		serverFid := m.allocFid()
		c.fids[msg.Fid] = serverFid
		serverAfid := muxNoFid
		if msg.Afid != muxNoFid {
			if sf, ok := c.fids[msg.Afid]; ok {
				serverAfid = sf
			}
		}
		serverTag := m.allocTag()
		m.pending[serverTag] = &pendingReq{
			client:    c,
			clientTag: msg.Tag,
			msgType:   proto.Tattach,
			serverFid: serverFid,
			clientFid: msg.Fid,
			freeOnErr: true,
		}
		m.mu.Unlock()
		msg.Fid = serverFid
		msg.Afid = serverAfid
		return m.writeServer(msg, serverTag)

	case *proto.TWalk:
		m.mu.Lock()
		serverFid, ok := c.fids[msg.Fid]
		if !ok {
			m.mu.Unlock()
			return m.sendError(c, msg.Tag, fmt.Sprintf("unknown fid %d", msg.Fid))
		}
		var serverNewFid uint32
		sameAs := msg.Newfid == msg.Fid
		if sameAs {
			serverNewFid = serverFid
		} else {
			serverNewFid = m.allocFid()
			c.fids[msg.Newfid] = serverNewFid
		}
		serverTag := m.allocTag()
		m.pending[serverTag] = &pendingReq{
			client:    c,
			clientTag: msg.Tag,
			msgType:   proto.Twalk,
			serverFid: serverNewFid,
			clientFid: msg.Newfid,
			freeOnErr: !sameAs, // only free if we allocated a new one
		}
		m.mu.Unlock()
		msg.Fid = serverFid
		msg.Newfid = serverNewFid
		return m.writeServer(msg, serverTag)

	case *proto.TOpen:
		return m.forwardFid(c, msg.Tag, proto.Topen, msg.Fid, msg)

	case *proto.TCreate:
		return m.forwardFid(c, msg.Tag, proto.Tcreate, msg.Fid, msg)

	case *proto.TRead:
		return m.forwardFid(c, msg.Tag, proto.Tread, msg.Fid, msg)

	case *proto.TWrite:
		return m.forwardFid(c, msg.Tag, proto.Twrite, msg.Fid, msg)

	case *proto.TStat:
		return m.forwardFid(c, msg.Tag, proto.Tstat, msg.Fid, msg)

	case *proto.TWstat:
		return m.forwardFid(c, msg.Tag, proto.Twstat, msg.Fid, msg)

	case *proto.TClunk:
		m.mu.Lock()
		serverFid, ok := c.fids[msg.Fid]
		if !ok {
			m.mu.Unlock()
			return m.sendError(c, msg.Tag, fmt.Sprintf("unknown fid %d", msg.Fid))
		}
		delete(c.fids, msg.Fid)
		serverTag := m.allocTag()
		m.pending[serverTag] = &pendingReq{
			client:     c,
			clientTag:  msg.Tag,
			msgType:    proto.Tclunk,
			serverFid:  serverFid,
			freeAlways: true,
		}
		m.mu.Unlock()
		msg.Fid = serverFid
		return m.writeServer(msg, serverTag)

	case *proto.TRemove:
		m.mu.Lock()
		serverFid, ok := c.fids[msg.Fid]
		if !ok {
			m.mu.Unlock()
			return m.sendError(c, msg.Tag, fmt.Sprintf("unknown fid %d", msg.Fid))
		}
		delete(c.fids, msg.Fid)
		serverTag := m.allocTag()
		m.pending[serverTag] = &pendingReq{
			client:     c,
			clientTag:  msg.Tag,
			msgType:    proto.Tremove,
			serverFid:  serverFid,
			freeAlways: true,
		}
		m.mu.Unlock()
		msg.Fid = serverFid
		return m.writeServer(msg, serverTag)

	case *proto.TFlush:
		return m.handleTFlush(c, msg)

	default:
		return fmt.Errorf("9p mux: unhandled T-message")
	}
}

// forwardFid looks up the server fid for clientFid, registers a pending entry,
// then writes the (already fid-mutated by the caller) message to the server.
// The caller must pass the original clientFid before mutating msg.
func (m *NinePMux) forwardFid(c *muxClient, clientTag uint16, msgType uint8, clientFid uint32, msg proto.FCall) error {
	m.mu.Lock()
	serverFid, ok := c.fids[clientFid]
	if !ok {
		m.mu.Unlock()
		return m.sendError(c, clientTag, fmt.Sprintf("unknown fid %d", clientFid))
	}
	serverTag := m.allocTag()
	m.pending[serverTag] = &pendingReq{
		client:    c,
		clientTag: clientTag,
		msgType:   msgType,
		serverFid: muxNoFid,
	}
	m.mu.Unlock()

	// Patch fid in-place on the struct before composing.
	switch v := msg.(type) {
	case *proto.TOpen:
		v.Fid = serverFid
	case *proto.TCreate:
		v.Fid = serverFid
	case *proto.TRead:
		v.Fid = serverFid
	case *proto.TWrite:
		v.Fid = serverFid
	case *proto.TStat:
		v.Fid = serverFid
	case *proto.TWstat:
		v.Fid = serverFid
	}
	return m.writeServer(msg, serverTag)
}

func (m *NinePMux) handleTFlush(c *muxClient, msg *proto.TFlush) error {
	m.mu.Lock()

	// Find the pending entry for the original message on the server side.
	var serverOldTag uint16
	found := false
	for st, req := range m.pending {
		if req.client == c && req.clientTag == msg.Oldtag && !req.isFlush {
			serverOldTag = st
			found = true
			break
		}
	}

	if !found {
		// Original already completed; respond immediately.
		m.mu.Unlock()
		rfl := &proto.RFlush{Header: proto.Header{Type: proto.Rflush, Tag: msg.Tag}}
		c.write(rfl, msg.Tag)
		return nil
	}

	// Mark original as flushed so its late-arriving R-message is discarded.
	m.pending[serverOldTag].flushed = true

	serverFlushTag := m.allocTag()
	m.pending[serverFlushTag] = &pendingReq{
		client:       c,
		clientTag:    msg.Tag,
		msgType:      proto.Tflush,
		isFlush:      true,
		serverOldTag: serverOldTag,
	}
	m.mu.Unlock()

	msg.Oldtag = serverOldTag
	return m.writeServer(msg, serverFlushTag)
}

func (m *NinePMux) sendError(c *muxClient, clientTag uint16, text string) error {
	re := &proto.RError{
		Header: proto.Header{Type: proto.Rerror, Tag: clientTag},
		Ename:  text,
	}
	c.write(re, clientTag)
	return nil
}

// cleanupClient flushes outstanding server-bound requests and clunks all open
// fids for the disconnected client. Cleanup messages are fire-and-forget:
// they have nil client so responses are discarded by handleRMessage.
func (m *NinePMux) cleanupClient(c *muxClient) {
	m.mu.Lock()
	delete(m.clients, c.id)
	c.conn.Close()

	// Flush all outstanding T-messages for this client.
	for serverTag, req := range m.pending {
		if req.client != c || req.isFlush || req.flushed {
			continue
		}
		req.flushed = true // discard original R-message
		flushTag := m.allocTag()
		m.pending[flushTag] = &pendingReq{
			client:       nil,
			clientTag:    0,
			msgType:      proto.Tflush,
			isFlush:      true,
			serverOldTag: serverTag,
		}
		tf := &proto.TFlush{
			Header: proto.Header{Type: proto.Tflush},
			Oldtag: serverTag,
		}
		m.mu.Unlock()
		m.writeServer(tf, flushTag) //nolint: errcheck
		m.mu.Lock()
	}

	// Clunk all server fids held by this client.
	for _, serverFid := range c.fids {
		clunkTag := m.allocTag()
		m.pending[clunkTag] = &pendingReq{
			client:     nil,
			clientTag:  0,
			msgType:    proto.Tclunk,
			serverFid:  serverFid,
			freeAlways: true,
		}
		tc := &proto.TClunk{
			Header: proto.Header{Type: proto.Tclunk},
			Fid:    serverFid,
		}
		m.mu.Unlock()
		m.writeServer(tc, clunkTag) //nolint: errcheck
		m.mu.Lock()
	}
	c.fids = nil
	m.mu.Unlock()
}

// Close tears down the server connection, which causes serverReader to exit
// and close all client connections.
func (m *NinePMux) Close() error {
	select {
	case <-m.done:
	default:
		close(m.done)
	}
	return m.conn.Close()
}
