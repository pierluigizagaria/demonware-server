// UDP relay for Crash Team Racing, modelled on the ACNH server's relay.go.
//
// The server introduces peers to each other (nat.go, 0x0A -> 0x0B) but does nothing when
// the hole punch fails, and for some pairs it always fails: measured on production, the
// address each client puts in its introduction request matches what we observe
// (mismatch=false), so the introduction is correct and the failures are genuine NAT
// incompatibility. Those players simply cannot reach each other directly.
//
// bdCommonAddr, the 67-byte hostAddr blob the game exchanges, already has room for this:
//
//	slot 0      local address   ip[4] + port u16 little-endian
//	slots 1..9  RELAY addresses, empty slots carry the sentinel 0.255.0.255:0
//	slot 10     public address
//	byte 66     NAT type
//
// Filling slot 1 with a relay endpoint is what the protocol expects.
//
// Demux: a single shared port cannot tell which peer a packet is meant for, so one port is
// allocated per (host, joiner) PAIR. That works because the session list is rewritten for
// each requester individually, so every joiner can be handed its own relay port.
//
// Only the joiner's copy is rewritten. The host keeps sending to whatever address it
// learned, and the relay forwards with itself as the source, so the host's replies come
// back to the relay on their own. The host side of the introduction (the 0x0B packet) is
// HMAC-protected and must not be touched.

package relay

import (
	binary "encoding/binary"
	diagnosticspkg "github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	net "net"
	strconv "strconv"
	strings "strings"
	sync "sync"
	time "time"
)

// relayFor limits WHICH joiners get a relay address written into their copy of a host's
// bdCommonAddr. Empty means nobody, so turning the ports on alone changes nothing for
// anyone; "*" means everyone. It exists so the relay can be proven on one account before
// it is allowed near the ~69% of rooms that already connect directly.
func (relay *RelayTable) relayFor(joinerPID uint64) bool {
	list := strings.TrimSpace(relay.Pids)
	if list == "" {
		return false
	}
	if list == "*" {
		return true
	}
	for _, f := range strings.Split(list, ",") {
		if n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 64); err == nil && n == joinerPID {
			return true
		}
	}
	return false
}

const (
	RelayAddrLen  = 6  // ip[4] + port u16
	RelaySlotLow  = 1  // first relay slot in bdCommonAddr
	RelaySlotHigh = 9  // last relay slot
	CommonAddrLen = 67 // local + 9 relay + public + nat type
)

// emptySlot is what the game writes into an unused relay slot.
var emptySlot = [4]byte{0, 255, 0, 255}

type RelayPair struct {
	table              *RelayTable
	port               int
	hostPID, joinerPID uint64
	host               *net.UDPAddr // the host's public endpoint, from its own hostAddr
	joiner             *net.UDPAddr // learned from the first packet that is not the host
	Conn               *net.UDPConn
}

type RelayTable struct {
	stopped       bool
	stop          chan struct{}
	workers       sync.WaitGroup
	reaperStarted bool
	*diagnosticspkg.Diagnostics
	BindAddr, Pids string
	mu             sync.Mutex
	Pairs          map[string]*RelayPair // "hostPID:joinerPID" -> pair
	ByPort         map[int]*RelayPair
	Low            int
	High           int
	Next           int
	Ip             net.IP
}

func NewRelayTable(diag *diagnosticspkg.Diagnostics, bindAddr, pids string) *RelayTable {
	return &RelayTable{Diagnostics: diag, BindAddr: bindAddr, Pids: pids, Pairs: map[string]*RelayPair{}, ByPort: map[int]*RelayPair{}}
}

func (relay *RelayTable) relayEnabled() bool { return relay.High >= relay.Low && relay.Low > 0 }

func (relay *RelayTable) StartCTRRelay(low, high int, host string, onlinePIDs func() []uint64) {
	ip := net.ParseIP(host).To4()
	if ip == nil || low <= 0 || high < low {
		relay.DiagnosticLogf("[relay] disabled (host=%q ports %d-%d)", host, low, high)
		return
	}
	relay.mu.Lock()
	if relay.stopped || relay.reaperStarted {
		relay.mu.Unlock()
		return
	}
	relay.reaperStarted = true
	relay.stop = make(chan struct{})
	relay.workers.Add(1)
	relay.Low, relay.High, relay.Next, relay.Ip = low, high, low, ip
	relay.mu.Unlock()
	relay.DiagnosticLogf("[relay] enabled on %s:%d-%d", ip, low, high)
	go func() { defer relay.workers.Done(); relay.reap(onlinePIDs) }()
}

// parseAddrSlot reads one bdCommonAddr slot. The port is little-endian, as in nat.go.
func ParseAddrSlot(b []byte) *net.UDPAddr {
	if len(b) < RelayAddrLen {
		return nil
	}
	if b[0] == emptySlot[0] && b[1] == emptySlot[1] && b[2] == emptySlot[2] && b[3] == emptySlot[3] {
		return nil
	}
	port := int(binary.LittleEndian.Uint16(b[4:6]))
	if port == 0 {
		return nil
	}
	return &net.UDPAddr{IP: net.IPv4(b[0], b[1], b[2], b[3]), Port: port}
}

func writeAddrSlot(b []byte, a *net.UDPAddr) {
	ip := a.IP.To4()
	copy(b[0:4], ip)
	binary.LittleEndian.PutUint16(b[4:6], uint16(a.Port))
}

// hostPublicAddr returns the public endpoint the host advertised (slot 10).
func HostPublicAddr(hostAddr []byte) *net.UDPAddr {
	if len(hostAddr) < CommonAddrLen {
		return nil
	}
	return ParseAddrSlot(hostAddr[60:66])
}

// withRelay returns a copy of hostAddr with a relay endpoint written into the first free
// relay slot, allocating a port for this (host, joiner) pair. The original is returned
// unchanged when the relay is off or the blob is not a bdCommonAddr.
func (relay *RelayTable) WithRelay(hostAddr []byte, hostPID, joinerPID uint64) []byte {
	if !relay.relayEnabled() || len(hostAddr) < CommonAddrLen || hostPID == 0 || joinerPID == 0 || hostPID == joinerPID {
		return hostAddr
	}
	if !relay.relayFor(joinerPID) {
		return hostAddr
	}
	dst := HostPublicAddr(hostAddr)
	if dst == nil {
		return hostAddr
	}
	p := relay.pairFor(hostPID, joinerPID, dst)
	if p == nil {
		return hostAddr
	}
	out := make([]byte, len(hostAddr))
	copy(out, hostAddr)
	for slot := RelaySlotLow; slot <= RelaySlotHigh; slot++ {
		off := slot * RelayAddrLen
		if ParseAddrSlot(out[off:off+RelayAddrLen]) != nil {
			continue // already occupied
		}
		writeAddrSlot(out[off:off+RelayAddrLen], &net.UDPAddr{IP: relay.Ip, Port: p.port})
		relay.DiagnosticLogf("[relay] offered :%d to pid=%d for host=%d (slot %d)", p.port, joinerPID, hostPID, slot)
		return out
	}
	return hostAddr
}

func PairKey(hostPID, joinerPID uint64) string {
	return string(binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, hostPID), joinerPID))
}

// pairFor returns the relay pair for these two players, opening a socket on first use.
func (t *RelayTable) pairFor(hostPID, joinerPID uint64, host *net.UDPAddr) *RelayPair {
	key := PairKey(hostPID, joinerPID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return nil
	}
	if p, ok := t.Pairs[key]; ok {
		p.host = host
		return p
	}
	for tries := 0; tries <= t.High-t.Low; tries++ {
		port := t.Next
		t.Next++
		if t.Next > t.High {
			t.Next = t.Low
		}
		if _, busy := t.ByPort[port]; busy {
			continue
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(t.BindAddr), Port: port})
		if err != nil {
			continue
		}
		p := &RelayPair{table: t, port: port, hostPID: hostPID, joinerPID: joinerPID, host: host, Conn: conn}
		t.Pairs[key], t.ByPort[port] = p, p
		t.workers.Add(1)
		go func() { defer t.workers.Done(); p.serve() }()
		t.DiagnosticLogf("[relay] pair %d<->%d on :%d (host %s)", hostPID, joinerPID, port, host)
		return p
	}
	t.DiagnosticLogf("[relay] no free port for %d<->%d", hostPID, joinerPID)
	return nil
}

// serve forwards in both directions. Anything from the host goes to the joiner; anything
// else is taken to be the joiner (learned on its first packet) and goes to the host.
func (p *RelayPair) serve() {
	buf := make([]byte, 2048)
	for {
		n, src, err := p.Conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.table.mu.Lock()
		host, joiner := p.host, p.joiner
		fromHost := host != nil && src.IP.Equal(host.IP) && src.Port == host.Port
		if !fromHost && (joiner == nil || !src.IP.Equal(joiner.IP) || src.Port != joiner.Port) {
			p.joiner, joiner = src, src
			p.table.DiagnosticLogf("[relay] :%d FIRST PACKET from %s (%d bytes) — the game DOES use relay slots",
				p.port, src, n)
		}
		p.table.mu.Unlock()

		var to *net.UDPAddr
		if fromHost {
			to = joiner
		} else {
			to = host
		}
		if to == nil {
			continue
		}
		_, _ = p.Conn.WriteToUDP(buf[:n], to)
	}
}

// reap releases a pair's port once either player has no lobby connection.
// A quiet pair between connected players is kept: races and menus may be
// idle for any length of time.
func (t *RelayTable) reap(onlinePIDs func() []uint64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
		}
		connected := map[uint64]bool{}
		for _, pid := range onlinePIDs() {
			connected[pid] = true
		}
		t.ReapOffline(connected)
	}
}

func (t *RelayTable) ReapOffline(online map[uint64]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, p := range t.Pairs {
		if online[p.hostPID] && online[p.joinerPID] {
			continue
		}
		_ = p.Conn.Close()
		delete(t.Pairs, key)
		delete(t.ByPort, p.port)
	}
}

func (t *RelayTable) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	if t.stop != nil {
		close(t.stop)
	}
	for _, pair := range t.Pairs {
		_ = pair.Conn.Close()
	}
}
func (t *RelayTable) Wait() { t.workers.Wait() }
