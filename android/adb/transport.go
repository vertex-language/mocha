package adb

import (
	"context"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// muxTransport is a single ADB transport connection (CNXN-handshaken) that
// multiplexes many logical streams over OPEN/OKAY/WRTE/CLSE, as used when
// this package talks directly to a device or emulator over TCP without
// going through a local adb server.
type muxTransport struct {
	conn    net.Conn
	maxData uint32

	nextID uint32

	mu      sync.Mutex
	streams map[uint32]*muxStream // keyed by our local-id
	closed  bool

	writeMu sync.Mutex
}

// dialDeviceTransport performs the TCP connection and CNXN/AUTH handshake
// against a device listening directly on addr (e.g. "192.168.1.5:5555" for
// wireless debugging, or the odd-numbered port of an emulator pair).
func dialDeviceTransport(ctx context.Context, addr string, key *rsa.PrivateKey) (*muxTransport, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("adb: dial %s: %w", addr, err)
	}

	t := &muxTransport{
		conn:    conn,
		maxData: connectMaxData,
		streams: make(map[uint32]*muxStream),
	}

	identity := "host::mocha\x00"
	if err := writeMessage(conn, message{
		command: cmdCNXN,
		arg0:    connectVersion,
		arg1:    connectMaxData,
		payload: []byte(identity),
	}); err != nil {
		conn.Close()
		return nil, err
	}

	for {
		m, err := readMessage(conn, connectMaxData)
		if err != nil {
			conn.Close()
			return nil, err
		}
		switch m.command {
		case cmdCNXN:
			if m.arg1 > 0 {
				t.maxData = m.arg1
			}
			go t.readPump()
			return t, nil
		case cmdAUTH:
			if m.arg0 != authToken {
				conn.Close()
				return nil, fmt.Errorf("%w: unexpected AUTH sub-type %d", ErrProtocol, m.arg0)
			}
			if key == nil {
				conn.Close()
				return nil, ErrAuthRequired
			}
			sig, err := signAuthToken(key, m.payload)
			if err != nil {
				conn.Close()
				return nil, err
			}
			if err := writeMessage(conn, message{command: cmdAUTH, arg0: authSignature, payload: sig}); err != nil {
				conn.Close()
				return nil, err
			}
			// Offer the public key too, in case the daemon doesn't yet
			// recognize this key from the raw signature alone; harmless
			// if the daemon ignores it and simply prompts on-device.
			if pub, err := marshalADBPublicKey(key); err == nil {
				_ = writeMessage(conn, message{command: cmdAUTH, arg0: authRSAPubKey, payload: pub})
			}
		default:
			conn.Close()
			return nil, fmt.Errorf("%w: expected CNXN or AUTH, got %s", ErrProtocol, cmdName(m.command))
		}
	}
}

func (t *muxTransport) readPump() {
	for {
		m, err := readMessage(t.conn, t.maxData)
		if err != nil {
			t.teardown(err)
			return
		}
		switch m.command {
		case cmdOKAY:
			t.dispatch(m.arg1, func(s *muxStream) { s.onReady(m.arg0) })
		case cmdWRTE:
			t.dispatch(m.arg1, func(s *muxStream) {
				s.onWrite(m.payload)
				// Flow control: ack immediately so the peer's next WRTE is
				// unblocked (protocol.txt: one outstanding WRTE per READY).
				_ = writeMessage(t.conn, message{command: cmdOKAY, arg0: s.localID, arg1: s.remoteID})
			})
		case cmdCLSE:
			t.dispatch(m.arg1, func(s *muxStream) { s.onClose() })
		default:
			// Unrecognized or out-of-place command: protocol.txt requires
			// closing the connection since state is now out of sync.
			t.teardown(fmt.Errorf("%w: unexpected %s on transport", ErrProtocol, cmdName(m.command)))
			return
		}
	}
}

func (t *muxTransport) dispatch(id uint32, fn func(s *muxStream)) {
	t.mu.Lock()
	s := t.streams[id]
	t.mu.Unlock()
	if s != nil {
		fn(s)
	}
}

func (t *muxTransport) teardown(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	streams := t.streams
	t.streams = nil
	t.mu.Unlock()

	for _, s := range streams {
		s.fail(err)
	}
	t.conn.Close()
}

// open starts a new logical stream to the named destination service (e.g.
// "shell:pm install -S 12345", "sync:") and blocks until the peer answers
// with READY (success) or CLOSE (failure).
func (t *muxTransport) open(ctx context.Context, service string) (*muxStream, error) {
	id := atomic.AddUint32(&t.nextID, 1)
	s := &muxStream{
		transport: t,
		localID:   id,
		ready:     make(chan struct{}),
		writes:    make(chan []byte, 16),
		closed:    make(chan struct{}),
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	t.streams[id] = s
	t.mu.Unlock()

	t.writeMu.Lock()
	err := writeMessage(t.conn, message{command: cmdOPEN, arg0: id, payload: []byte(service + "\x00")})
	t.writeMu.Unlock()
	if err != nil {
		t.removeStream(id)
		return nil, err
	}

	select {
	case <-s.ready:
		if s.failErr != nil {
			t.removeStream(id)
			return nil, s.failErr
		}
		return s, nil
	case <-ctx.Done():
		t.removeStream(id)
		return nil, ctx.Err()
	}
}

func (t *muxTransport) removeStream(id uint32) {
	t.mu.Lock()
	if t.streams != nil {
		delete(t.streams, id)
	}
	t.mu.Unlock()
}

func (t *muxTransport) close() error {
	t.teardown(ErrClosed)
	return nil
}

// muxStream is one OPEN/OKAY/WRTE/CLSE-multiplexed logical stream.
type muxStream struct {
	transport *muxTransport
	localID   uint32
	remoteID  uint32

	readyOnce sync.Once
	ready     chan struct{}
	failErr   error

	writes chan []byte
	buf    []byte

	closeOnce sync.Once
	closed    chan struct{}
}

func (s *muxStream) onReady(remoteID uint32) {
	s.remoteID = remoteID
	s.readyOnce.Do(func() { close(s.ready) })
}

func (s *muxStream) onWrite(payload []byte) {
	data := append([]byte(nil), payload...)
	select {
	case s.writes <- data:
	case <-s.closed:
	}
}

func (s *muxStream) onClose() {
	s.closeOnce.Do(func() { close(s.closed) })
	s.readyOnce.Do(func() { s.failErr = ErrStreamClosed; close(s.ready) })
}

func (s *muxStream) fail(err error) {
	s.failErr = err
	s.closeOnce.Do(func() { close(s.closed) })
	s.readyOnce.Do(func() { close(s.ready) })
}

func (s *muxStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		select {
		case data, ok := <-s.writes:
			if !ok {
				return 0, io.EOF
			}
			s.buf = data
		case <-s.closed:
			select {
			case data := <-s.writes:
				s.buf = data
			default:
				if s.failErr != nil && s.failErr != ErrStreamClosed {
					return 0, s.failErr
				}
				return 0, io.EOF
			}
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *muxStream) Write(p []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, ErrStreamClosed
	default:
	}
	if err := writeMessage(s.transport.conn, message{command: cmdWRTE, arg1: s.remoteID, payload: p}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *muxStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = writeMessage(s.transport.conn, message{command: cmdCLSE, arg0: s.localID, arg1: s.remoteID})
	})
	s.transport.removeStream(s.localID)
	return nil
}