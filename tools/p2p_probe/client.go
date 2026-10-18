// The optional local integration fixture is driven by run.py. Credentials arrive
// only through inherited stdin; output contains fixed assertions and booleans.
package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
)

func must(ok bool, step string) {
	if !ok {
		panic(step)
	}
}

type input struct {
	Token, Address, GameBind, Name, PublicKey string
	PID                                       uint64
	Party                                     bool
	Loopback, WeakNativeToken, MixedEconomy   bool
	Index                                     int
	Canonical                                 endpoint
}
type endpoint struct {
	Address string
	Port    int
}

var control = json.NewDecoder(os.Stdin)

func bridge(request map[string]any, reply any) {
	request["bridge"] = true
	must(json.NewEncoder(os.Stdout).Encode(request) == nil, "fixture bridge request")
	must(control.Decode(reply) == nil, "fixture bridge reply")
}

func (p *peer) route(direction int, input *net.UDPAddr) *net.UDPAddr {
	if p.config.Loopback {
		// An unmodified host uses the canonical socket directly. Inbound
		// identity assertions below inspect the actual OS datagram source.
		return input
	}
	var reply struct {
		Status  int
		Address string
		Port    int
	}
	bridge(map[string]any{"operation": "route", "index": p.config.Index, "direction": direction, "address": input.IP.String(), "port": input.Port}, &reply)
	must(reply.Status == 0 && reply.Port > 0, "authorized endpoint route")
	return &net.UDPAddr{IP: net.ParseIP(reply.Address), Port: reply.Port}
}

func commonAddress(c input) []byte {
	a := make([]byte, 67)
	for slot := 0; slot < 11; slot++ {
		if slot > 0 && slot < 10 {
			copy(a[slot*6:], []byte{0, 255, 0, 255})
			continue
		}
		copy(a[slot*6:], net.ParseIP(c.Canonical.Address).To4())
		port := 3074
		if slot == 10 {
			port = c.Canonical.Port
		}
		binary.LittleEndian.PutUint16(a[slot*6+4:], uint16(port))
	}
	a[66] = 3
	return a
}

func (p *peer) verifyDiscovery() {
	server := &net.UDPAddr{IP: net.ParseIP(p.config.Address), Port: 3074}
	_, err := p.udp.WriteToUDP([]byte{30, 3, 0}, server)
	must(err == nil, "canonical discovery send")
	buf := make([]byte, 64)
	p.udp.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, source, err := p.udp.ReadFromUDP(buf)
	want := commonAddress(p.config)[60:66]
	must(err == nil && source.IP.Equal(server.IP) && source.Port == server.Port && n == 9 && bytes.Equal(buf[:3], []byte{31, 2, 0}) && bytes.Equal(buf[3:n], want), "canonical discovery exact source")
}

type peer struct {
	conn   net.Conn
	keys   demonware.SessionKeys
	seq    uint32
	pushes [][]byte
	config input
	udp    *net.UDPConn
}

func (p *peer) frame() []byte {
	p.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	raw, err := demonware.ReadFrame(func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(p.conn, b); return b, err }, 65536)
	must(err == nil, "read frame")
	return raw
}
func connect(c input) *peer {
	t, _ := json.Marshal(map[string]string{"id_token": c.Token})
	e, _ := json.Marshal(map[string]string{"token": string(t)})
	body, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(e)})
	h := &http.Client{Timeout: 10 * time.Second}
	if c.WeakNativeToken {
		foreignToken, _ := json.Marshal(map[string]string{"id_token": fmt.Sprintf("sacredfire.Same.Name.%016x", c.PID+123)})
		foreignExtra, _ := json.Marshal(map[string]string{"token": string(foreignToken)})
		foreignBody, _ := json.Marshal(map[string]string{"title_id": "5760", "auth_task": "46", "iv_seed": "1", "extra_data": string(foreignExtra)})
		refused, err := h.Post("http://"+c.Address+":8460/auth/", "application/json", bytes.NewReader(foreignBody))
		must(err == nil, "foreign native token request")
		refused.Body.Close()
		must(refused.StatusCode == http.StatusForbidden, "foreign native account refused")
	}
	r, err := h.Post("http://"+c.Address+":8460/auth/", "application/json", bytes.NewReader(body))
	must(err == nil, "local HTTP request")
	defer r.Body.Close()
	must(r.StatusCode == 200 && r.Header.Get("X-Signature") != "", "signed HTTP authentication")
	response, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	must(err == nil && len(response) <= 65536, "bounded authentication response")
	publicDER, err := base64.StdEncoding.DecodeString(c.PublicKey)
	must(err == nil, "fixture public key")
	public, err := x509.ParsePKIXPublicKey(publicDER)
	must(err == nil, "parse public key")
	key, ok := public.(*rsa.PublicKey)
	must(ok, "RSA public key")
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
	digest := sha256.Sum256(response)
	must(err == nil && rsa.VerifyPSS(key, crypto.SHA256, digest[:], signature, nil) == nil, "authentication signature verification")
	var answer map[string]string
	must(json.Unmarshal(response, &answer) == nil && answer["code"] == "700", "authentication code700")
	ticket, err := base64.StdEncoding.DecodeString(answer["server_ticket"])
	must(err == nil && len(ticket) == 128, "server ticket")
	conn, err := net.DialTimeout("tcp", c.Address+":3074", 3*time.Second)
	must(err == nil, "lobby connect")
	p := &peer{conn: conn, config: c}
	nonce := make([]byte, 8)
	rand.Read(nonce)
	hello := []byte{}
	for _, v := range []uint32{200, 200, 210, 220, 65536} {
		hello = append(hello, demonware.Uint32LE(v)...)
	}
	hello = append(hello, nonce...)
	_, err = conn.Write(hello)
	must(err == nil, "send hello")
	server := p.frame()
	must(server[5] == demonware.MsgServerHi, "server hello")
	auth := demonware.Frame(append(append([]byte{demonware.MsgClientAuth}, ticket...), make([]byte, 8)...))
	p.keys = demonware.DeriveKeys(demonware.HandshakeTranscript(65536, nonce, server[6:], auth[:len(auth)-8]), ticket[97:121])
	copy(auth[len(auth)-8:], p.keys.ClientTag[:])
	conn.Write(auth)
	serverOK := p.frame()
	must(serverOK[5] == demonware.MsgServerOK && bytes.Equal(serverOK[6:], p.keys.ServerCheck[:]), "lobby handshake")
	p.udp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(c.GameBind), Port: 3074})
	must(err == nil, "game UDP bind")
	return p
}
func (p *peer) read() demonware.Message {
	raw := p.frame()
	f, err := demonware.ParseEncryptedFrame(raw)
	must(err == nil && f.Verify(p.keys.S2CMAC), "server encrypted MAC")
	message, err := f.Decrypt(p.keys.S2CAES)
	must(err == nil, "server decrypt")
	return message
}
func (p *peer) call(service, task byte, w *demonware.Writer) []byte {
	p.seq++
	payload := []byte{service, 3, task}
	if w != nil {
		payload = append(payload, w.Bytes()...)
	}
	iv := make([]byte, 16)
	rand.Read(iv)
	raw, err := demonware.EncodeEncrypted(p.seq, demonware.InnerTaskRequest, payload, iv, p.keys.C2SAES, p.keys.C2SMAC)
	must(err == nil, "encode request")
	p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = p.conn.Write(raw)
	must(err == nil, "send request")
	for {
		m := p.read()
		if m.Type == demonware.InnerPush {
			p.pushes = append(p.pushes, m.Payload)
			continue
		}
		must(m.Type == demonware.InnerTaskReply, "reply type")
		r := demonware.NewReader(m.Payload)
		_, _ = r.Uint64()
		code, err := r.Uint32()
		must(err == nil && code == 0, fmt.Sprintf("task%d/%d", service, task))
		return m.Payload
	}
}
func (p *peer) push() []byte {
	if len(p.pushes) > 0 {
		raw := p.pushes[0]
		p.pushes = p.pushes[1:]
		return raw
	}
	m := p.read()
	must(m.Type == demonware.InnerPush, "expected push")
	return m.Payload
}
func result(raw []byte) *demonware.Reader {
	r := demonware.NewReader(raw)
	_, _ = r.Uint64()
	_, _ = r.Uint32()
	_, _ = r.Uint8()
	count, err := r.Uint32()
	must(err == nil, "result count")
	if count > 0 {
		_, _ = r.Uint32()
	}
	return r
}
func info(c input) string {
	a := commonAddress(c)
	id := make([]byte, 8)
	key := make([]byte, 16)
	rand.Read(id)
	rand.Read(key)
	raw, _ := json.Marshal(map[string]any{"listen_server": map[string]string{"local_address": base64.StdEncoding.EncodeToString(a), "security_id": base64.StdEncoding.EncodeToString(id), "security_key": base64.StdEncoding.EncodeToString(key)}})
	return string(raw)
}
func args(s string) *demonware.Writer { w := &demonware.Writer{}; w.String(s); return w }
func main() {
	defer func() {
		if step := recover(); step != nil {
			name, ok := step.(string)
			if !ok {
				name = "runtime assertion"
			}
			raw, _ := json.Marshal(map[string]any{"ok": false, "step": name})
			fmt.Println(string(raw))
			os.Exit(1)
		}
	}()
	var cfg []input
	must(control.Decode(&cfg) == nil && len(cfg) == 2, "input")
	a := connect(cfg[0])
	defer a.conn.Close()
	defer a.udp.Close()
	b := connect(cfg[1])
	defer b.conn.Close()
	defer b.udp.Close()
	a.verifyDiscovery()
	b.verifyDiscovery()
	// Original friend result layout, queried before an async room exists.
	attrs := &demonware.Writer{}
	attrs.Uint64(cfg[0].PID)
	attrs.String(cfg[0].Name)
	create := args("Delta")
	create.Blob(commonAddress(cfg[0]))
	create.Uint32(1)
	create.Uint32(8)
	create.Blob(attrs.Bytes())
	a.call(138, 1, create)

	time.Sleep(100 * time.Millisecond)
	lookup := args("Delta")
	lookup.Uint64(cfg[0].PID)
	friend := result(b.call(138, 14, lookup))
	friendAddress, err := friend.Blob()
	must(err == nil && len(friendAddress) == 67, "friend advertisement")
	must(bytes.Equal(friendAddress, commonAddress(cfg[0])), "friend CommonAddr identity preserved")
	var identityReply struct{ OK bool }
	bridge(map[string]any{"operation": "identity", "self": base64.StdEncoding.EncodeToString(commonAddress(cfg[0])), "advertised": base64.StdEncoding.EncodeToString(friendAddress), "other": base64.StdEncoding.EncodeToString(commonAddress(cfg[1]))}, &identityReply)
	must(identityReply.OK, "original CommonAddr receive gate")
	// Only B read A's advertisement. A has neither looked up B nor allocated
	// a peer endpoint. Exercise the first datagram before any directory room.
	canonicalA := &net.UDPAddr{IP: net.ParseIP(cfg[0].Canonical.Address), Port: cfg[0].Canonical.Port}
	canonicalB := &net.UDPAddr{IP: net.ParseIP(cfg[1].Canonical.Address), Port: cfg[1].Canonical.Port}
	preRoomProxy := b.route(1, canonicalA)
	must(preRoomProxy.IP.IsLoopback(), "pre-room local proxy")
	_, err = b.udp.WriteToUDP([]byte("pre-room"), preRoomProxy)
	must(err == nil, "unilateral pre-room send")
	a.udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	preRoomBytes := make([]byte, 128)
	preRoomCount, reverseProxy, err := a.udp.ReadFromUDP(preRoomBytes)
	must(err == nil && string(preRoomBytes[:preRoomCount]) == "pre-room" && reverseProxy.IP.IsLoopback(), "unilateral pre-room delivery")
	fromB := a.route(2, reverseProxy)
	must(fromB.IP.Equal(canonicalB.IP) && fromB.Port == canonicalB.Port, "reverse source canonical identity")
	replyProxy := a.route(1, fromB)
	must(replyProxy.IP.Equal(reverseProxy.IP) && replyProxy.Port == reverseProxy.Port, "stable reverse proxy route")
	_, err = a.udp.WriteToUDP([]byte("pre-room-return"), replyProxy)
	must(err == nil, "unilateral pre-room reply")
	b.udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	preRoomCount, replySource, err := b.udp.ReadFromUDP(preRoomBytes)
	must(err == nil && string(preRoomBytes[:preRoomCount]) == "pre-room-return", "unilateral pre-room return")
	fromA := b.route(2, replySource)
	must(fromA.IP.Equal(canonicalA.IP) && fromA.Port == canonicalA.Port, "return source canonical identity")
	names := &demonware.Writer{}
	names.Uint32(1)
	names.String(cfg[0].Name)
	nr := result(b.call(12, 10, names))
	pid, err := nr.Uint64()
	must(err == nil && pid == cfg[0].PID, "local Steam name mapping")
	// Progress remains local, even when another peer has a leaderboard row.
	stat := &demonware.Writer{}
	stat.Uint32(89)
	stat.Uint64(cfg[0].PID)
	stat.Uint8(4)
	stat.Int64(100)
	stat.Uint32(0)
	a.call(4, 1, stat)
	read := &demonware.Writer{}
	read.Uint32(89)
	read.Uint32(1)
	read.Uint64(cfg[0].PID)
	read.Uint32(0)
	localRow := result(a.call(4, 13, read))
	storedPID, err := localRow.Uint64()
	storedScore, scoreError := localRow.Int64()
	must(err == nil && scoreError == nil && storedPID == cfg[0].PID && storedScore == 100, "local statistics write/read")
	foreign := b.call(4, 13, read)
	rr := demonware.NewReader(foreign)
	_, _ = rr.Uint64()
	_, _ = rr.Uint32()
	_, _ = rr.Uint8()
	n, _ := rr.Uint32()
	must(n == 0, "remote statistics privacy")
	a.call(145, 2, args(info(cfg[0])))
	b.call(145, 2, args(info(cfg[1])))
	members := fmt.Sprint(cfg[0].PID)
	if cfg[0].Party {
		members += "," + fmt.Sprint(cfg[1].PID)
	}
	a.call(145, 18, args(fmt.Sprintf(`{"members":[%s],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`, members)))
	if !cfg[0].Party {
		b.call(145, 18, args(fmt.Sprintf(`{"members":[%d],"lobby_slots":{"min":2,"max":8},"ruleset_payload":{"filter":{"playlist_id":0,"matchmaking_version":200}}}`, cfg[1].PID)))
	}
	r := demonware.NewReader(a.push())
	event, _ := r.Uint32()
	must(event == 2005, "owner create event")
	_, _ = r.Uint64()
	room, _ := r.Uint64()
	version, _ := r.Uint64()
	host, _ := r.String()
	_, _ = r.String()
	sync := &demonware.Writer{}
	sync.Uint64(room)
	sync.Uint64(version)
	sync.String(host)
	a.call(145, 15, sync)
	addr := friendAddress // A party follower uses the game's existing party propagation.
	if !cfg[0].Party {
		r = demonware.NewReader(b.push())
		event, _ = r.Uint32()
		must(event == 2001, "member join event")
		_, _ = r.Uint64()
		_, _ = r.Uint64()
		host, _ = r.String()
		var doc struct {
			Listen struct {
				Address string `json:"host_address"`
			} `json:"listen_server"`
		}
		must(json.Unmarshal([]byte(host), &doc) == nil, "host document")
		addr, err = base64.StdEncoding.DecodeString(doc.Listen.Address)
		must(err == nil && len(addr) == 67, "proxy address")
	}
	must(bytes.Equal(addr, commonAddress(cfg[0])), "room CommonAddr identity preserved")
	proxy := b.route(1, canonicalA)
	must(proxy.IP.IsLoopback(), "loopback proxy")
	payload := []byte{0, 255, 1, 2, 3, 4, 5}
	b.udp.WriteToUDP(payload, proxy)
	a.udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 128)
	nbytes, source, err := a.udp.ReadFromUDP(buf)
	must(err == nil && bytes.Equal(buf[:nbytes], payload) && source.IP.IsLoopback(), "lab race proxy delivery")
	fromB = a.route(2, source)
	must(fromB.IP.Equal(canonicalB.IP) && fromB.Port == canonicalB.Port, "room reverse identity")
	a.udp.WriteToUDP([]byte("return"), a.route(1, fromB))
	b.udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	nbytes, source, err = b.udp.ReadFromUDP(buf)
	must(err == nil && string(buf[:nbytes]) == "return", "lab return datagram")
	fromA = b.route(2, source)
	must(fromA.IP.Equal(canonicalA.IP) && fromA.Port == canonicalA.Port, "room return identity")
	// Disconnect without another room RPC: the party follower has no remote
	// request object on the owner, but its directory departure must free its seat.
	b.conn.Close()
	r = demonware.NewReader(a.push())
	event, _ = r.Uint32()
	must(event == 2006, "member departure update event")
	_, _ = r.Uint64()
	_, _ = r.Uint64()
	_, _ = r.String()
	backend, err := r.String()
	var membership struct {
		Players map[string]string `json:"players"`
	}
	must(err == nil && json.Unmarshal([]byte(backend), &membership) == nil && len(membership.Players) == 1 && membership.Players[strconv.FormatUint(cfg[0].PID, 10)] == "expected", "departed member seat released")
	output, _ := json.Marshal(map[string]any{"ok": true, "evidence": "implementation fixture, simulated lab", "publishers": 2, "signed_auth": true, "encrypted_lobby": true, "friend_advertisement": true, "friend_ad_before_party": !cfg[0].Party, "pre_room_bidirectional_proxy": true, "local_names": true, "private_statistics": true, "owner_create_member_join": !cfg[0].Party, "committed_party_search": cfg[0].Party, "bidirectional_proxy": true, "member_departure": true, "canonical_discovery": true, "exact_commonaddr_preserved": true, "canonical_routes_both_directions": true, "loopback_without_host_translation": cfg[0].Loopback, "weak_native_token_owner_only": cfg[0].WeakNativeToken, "mixed_economy": cfg[0].MixedEconomy})
	fmt.Println(string(output))
}
