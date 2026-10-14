// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.

package main

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
	"github.com/pierluigizagaria/demonware-server/internal/dashboard"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diablo"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
	"github.com/pierluigizagaria/demonware-server/internal/relay"
)

// Legacy integration fixtures retain component handles outside the production
// session. The session itself has no dependency on CTR or Diablo implementations.
type lobbyDependencies struct {
	files      *diablo.PublisherFiles
	relay      *relay.Table
	matchmaker *ctr.Matchmaker
	registry   *lobby.SessionRegistry
	ctr        *ctr.Services
	pubDir     string
	lobbyDoc   string
}

type lobbyConn struct {
	callbacksIssued, callbacksCompleted int
	replyError                          error
	C                                   net.Conn               // Test-owned switchable destination, never the production socket.
	Keys                                *demonware.SessionKeys // Derived independently by the fixture client.
	*lobby.Session
	lobbyDependencies
}

func newTestLobby(session *lobby.Session, dependencies lobbyDependencies) *lobbyConn {
	fixture := &lobbyConn{lobbyDependencies: dependencies, C: &platformWire{}}
	server := lobby.NewServer(config.Config{}, session.Table, session.Diagnostics, session.Tickets, session.Presence, session.ReplyCounter, &atomic.Uint64{}, session.Telemetry, session.Options)
	fixture.Session = server.NewConn(&fixtureConnection{target: &fixture.C}, "")
	fixture.N, fixture.Clock, fixture.Entropy = session.N, session.Clock, session.Entropy
	// Establish synthetic transport through the real proof path. Its temporary
	// ticket/presence owners do not contaminate the composed test's stores.
	tickets, players, options := fixture.Tickets, fixture.Presence, fixture.Options
	fixture.Tickets, fixture.Presence, fixture.Options = auth.NewTicketStore(), presence.NewStore(nil), &lobby.Options{}
	ticket := psnTicket(time.Now())
	fixture.Tickets.RememberTicket(ticket, platform.PlayerID{PID: 17, Title: 5760}, time.Now(), ps4.TicketWindow)
	raw, keys := fixtureClientAuth(ticket)
	if !fixture.OnClientAuth(raw) || !fixture.ChannelSnapshot().Established {
		panic("synthetic session proof failed")
	}
	fixture.Keys = &keys
	fixture.Tickets, fixture.Presence, fixture.Options, fixture.Player = tickets, players, options, session.Player
	fixture.C = &platformWire{}
	return fixture
}

func withTestRegistry(t *testing.T) {
	t.Helper()
	old := testRegistry
	testRegistry = lobby.NewSessionRegistry()
	t.Cleanup(func() { testRegistry = old })
}

func observedSession(registry *lobby.SessionRegistry, id [8]byte) *dashboard.SessionSnapshot {
	for _, entry := range registry.Snapshot() {
		if entry.ID == id {
			return &entry
		}
	}
	return nil
}

// fixtureConnection follows the fixture's destination so injected write errors
// never require changing a production session's private connection.
type fixtureConnection struct{ target *net.Conn }

func (c *fixtureConnection) Read(p []byte) (int, error)        { return (*c.target).Read(p) }
func (c *fixtureConnection) Write(p []byte) (int, error)       { return (*c.target).Write(p) }
func (c *fixtureConnection) Close() error                      { return (*c.target).Close() }
func (c *fixtureConnection) LocalAddr() net.Addr               { return (*c.target).LocalAddr() }
func (c *fixtureConnection) RemoteAddr() net.Addr              { return (*c.target).RemoteAddr() }
func (c *fixtureConnection) SetDeadline(t time.Time) error     { return (*c.target).SetDeadline(t) }
func (c *fixtureConnection) SetReadDeadline(t time.Time) error { return (*c.target).SetReadDeadline(t) }
func (c *fixtureConnection) SetWriteDeadline(t time.Time) error {
	return (*c.target).SetWriteDeadline(t)
}

func fixtureClientAuth(ticket []byte) ([]byte, demonware.SessionKeys) {
	raw := demonware.Frame(append(append([]byte{demonware.MsgClientAuth}, ticket...), make([]byte, 8)...))
	// Direct-handler fixtures begin before a socket hello, with a zero transcript
	// prefix. Socket-level tests in lobby cover the complete hello exchange.
	transcript := demonware.HandshakeTranscript(0, nil, nil, raw[:len(raw)-8])
	keys := demonware.DeriveKeys(transcript, ticket[97:121])
	copy(raw[len(raw)-8:], keys.ClientTag[:])
	return raw, keys
}
