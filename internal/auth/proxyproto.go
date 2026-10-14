// PROXY protocol v1 on the auth port, as nextendo-nex does for the NEX auth
// servers (nextendo-nex proxyproto.go): sni-router passes TLS through untouched,
// so without it the auth sees the router (127.0.0.1) instead of the player. With
// SNI_PROXY_PROTOCOL=1 the router prepends, before the ClientHello,
//
//	PROXY TCP4 <srcIP> <dstIP> <srcPort> <dstPort>\r\n
//
// and NEXTENDO_PROXY_PROTOCOL=1 here reads it. Connections without the header
// pass through unchanged. The header is read on a per-connection goroutine with
// a deadline, so a client that sends nothing cannot stall the accept loop.

package auth

import (
	bufio "bufio"
	net "net"
	strconv "strconv"
	strings "strings"
	"sync"
	time "time"
)

const proxyHeaderTimeout = 10 * time.Second

// proxyConn overrides RemoteAddr with the client address from the PROXY header.
type proxyConn struct {
	net.Conn
	reader     *bufio.Reader
	remoteAddr net.Addr
}

func (c *proxyConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func (c *proxyConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return c.Conn.RemoteAddr()
}

type proxyListener struct {
	net.Listener
	conns   chan net.Conn
	errc    chan error
	done    chan struct{}
	mu      sync.Mutex
	closed  bool
	pending map[net.Conn]struct{}
	workers sync.WaitGroup
}

func NewProxyListener(ln net.Listener) *proxyListener {
	l := &proxyListener{Listener: ln, conns: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{}), pending: map[net.Conn]struct{}{}}
	l.workers.Add(1)
	go l.acceptLoop()
	return l
}

func (l *proxyListener) acceptLoop() {
	defer l.workers.Done()
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			select {
			case l.errc <- err:
			case <-l.done:
			}
			return
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			c.Close()
			return
		}
		l.pending[c] = struct{}{}
		l.workers.Add(1)
		l.mu.Unlock()
		go func(c net.Conn) {
			defer l.workers.Done()
			defer func() { l.mu.Lock(); delete(l.pending, c); l.mu.Unlock() }()
			pc, err := readProxyHeader(c)
			if err != nil {
				c.Close()
				return
			}
			select {
			case l.conns <- pc:
			case <-l.done:
				c.Close()
			}
		}(c)
	}
}

func (l *proxyListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		l.mu.Lock()
		delete(l.pending, c.(*proxyConn).Conn)
		closed := l.closed
		l.mu.Unlock()
		if closed {
			c.Close()
			return nil, net.ErrClosed
		}
		return c, nil
	case err := <-l.errc:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close interrupts headers and undelivered handoffs, never an accepted HTTP
// connection. Wait joins both the accept loop and every header worker.
func (l *proxyListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.done)
	for c := range l.pending {
		_ = c.Close()
	}
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *proxyListener) Wait() { l.workers.Wait() }

// readProxyHeader consumes a PROXY v1 line if the connection starts with one.
func readProxyHeader(c net.Conn) (*proxyConn, error) {
	pc := &proxyConn{Conn: c, reader: bufio.NewReader(c)}
	_ = c.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
	defer c.SetReadDeadline(time.Time{})

	head, err := pc.reader.Peek(6)
	if err != nil {
		return nil, err
	}
	if string(head) != "PROXY " {
		return pc, nil
	}
	// ReadSlice is bounded by the reader's buffer (a v1 line is at most 107 bytes).
	line, err := pc.reader.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(strings.TrimSpace(string(line)))
	// f = [PROXY, TCP4|TCP6|UNKNOWN, srcIP, dstIP, srcPort, dstPort]
	if len(f) >= 6 && (f[1] == "TCP4" || f[1] == "TCP6") {
		port, _ := strconv.Atoi(f[4])
		if ip := net.ParseIP(f[2]); ip != nil {
			pc.remoteAddr = &net.TCPAddr{IP: ip, Port: port}
		}
	}
	return pc, nil
}
