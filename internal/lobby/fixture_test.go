package lobby

import (
	"bytes"
	"net"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

type sessionWire struct{ bytes.Buffer }

func (*sessionWire) Close() error                     { return nil }
func (*sessionWire) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*sessionWire) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*sessionWire) SetDeadline(time.Time) error      { return nil }
func (*sessionWire) SetReadDeadline(time.Time) error  { return nil }
func (*sessionWire) SetWriteDeadline(time.Time) error { return nil }

type fixtureTelemetry struct{}

func (*fixtureTelemetry) RegisterConn(uint64, string)         {}
func (*fixtureTelemetry) UnregisterConn(uint64)               {}
func (*fixtureTelemetry) NoteTask(uint64, uint64, byte, byte) {}

func sharedPS4Lobby() (*Session, *sessionWire) {
	wire := &sessionWire{}
	keys := demonware.DeriveKeys([]byte("synthetic fixture"), make([]byte, 24))
	replies := &ReplyCounter{}
	return &Session{conn: wire, N: 1, keys: &keys, Player: &platform.PlayerID{PID: 17, Title: 5760},
		Diagnostics: &diagnostics.Diagnostics{RedactLogs: true}, Telemetry: &fixtureTelemetry{}, Options: &Options{},
		ReplyCounter: replies, Table: &ServiceTable{Replies: replies}, Tickets: auth.NewTicketStore(), Presence: presence.NewStore(nil)}, wire
}
