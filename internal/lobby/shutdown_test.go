package lobby

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/diagnostics"
	"github.com/pierluigizagaria/demonware-server/internal/presence"
)

type shutdownTelemetry struct {
	registered   chan struct{}
	unregistered chan struct{}
}

func (m *shutdownTelemetry) RegisterConn(uint64, string)       { close(m.registered) }
func (m *shutdownTelemetry) UnregisterConn(uint64)             { close(m.unregistered) }
func (*shutdownTelemetry) NoteTask(uint64, uint64, byte, byte) {}

type shutdownHandler func(TaskSession, TaskRequest) TaskResult

func (h shutdownHandler) Handle(s TaskSession, r TaskRequest) TaskResult { return h(s, r) }

func awaitShutdown(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown worker did not finish")
	}
}

func TestShutdownInterruptsIdleAndPartialLobbyReads(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "partial"}[partial], func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			keys := demonware.DeriveKeys([]byte("synthetic transcript"), make([]byte, 24))
			l := &Session{conn: server, keys: &keys}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if _, err := l.readFull(4); err == nil {
					t.Error("shutdown read succeeded")
				}
			}()
			if partial {
				if _, err := client.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			}
			l.RequestStop()
			awaitShutdown(t, done)
			if _, err := l.readFull(4); err == nil {
				t.Fatal("subsequent read reset the shutdown deadline")
			}
		})
	}
}

func TestShutdownFinishesAdmittedReplyAndCallback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "write-failed"}[fail], func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			keys := demonware.DeriveKeys([]byte("synthetic transcript"), make([]byte, 24))
			replies := &ReplyCounter{}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			calls, callbacks := 0, 0
			want := replies.TaskReply(1, 0, nil)
			handler := shutdownHandler(func(TaskSession, TaskRequest) TaskResult {
				calls++
				close(entered)
				<-release
				return TaskResult{Reply: want, AfterReply: func(err error) {
					callbacks++
					if (err != nil) != fail {
						t.Error("callback did not receive send outcome")
					}
				}}
			})
			l := &Session{conn: server, keys: &keys, Diagnostics: &diagnostics.Diagnostics{RedactLogs: true}, ReplyCounter: replies,
				Presence: presence.NewStore(nil), Telemetry: &shutdownTelemetry{}, Table: &ServiceTable{Replies: replies, Handlers: map[byte]TaskHandler{1: handler}}}
			go func() { defer close(done); l.ProcessTask([]byte{1, 3, 1}) }()
			awaitShutdown(t, entered)
			l.RequestStop()
			if fail {
				client.Close()
			}
			close(release)
			if !fail {
				client.SetReadDeadline(time.Now().Add(2 * time.Second))
				raw, err := demonware.ReadFrame(func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(client, b); return b, err }, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				frame, err := demonware.ParseEncryptedFrame(raw)
				if err != nil {
					t.Fatal(err)
				}
				if frame.Sequence != 1 || !frame.Verify(keys.S2CMAC) {
					t.Fatal("invalid reply sequence/MAC")
				}
				msg, err := frame.Decrypt(keys.S2CAES)
				if err != nil || msg.Type != demonware.InnerTaskReply || !bytes.Equal(msg.Payload, want) {
					t.Fatal("reply changed during shutdown")
				}
			}
			awaitShutdown(t, done)
			l.ProcessTask([]byte{1, 3, 1})
			if calls != 1 || callbacks != 1 {
				t.Fatalf("handlers=%d callbacks=%d", calls, callbacks)
			}
		})
	}
}

type shutdownListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *shutdownListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *shutdownListener) Close() error { l.once.Do(func() { close(l.done) }); return nil }
func (*shutdownListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestLobbyShutdownJoinsRegisteredConnections(t *testing.T) {
	monitor := &shutdownTelemetry{registered: make(chan struct{}), unregistered: make(chan struct{})}
	s := &Server{Diag: &diagnostics.Diagnostics{RedactLogs: true}, ids: &atomic.Uint64{}, Monitor: monitor,
		Table: &ServiceTable{}, Presence: presence.NewStore(nil)}
	ln := &shutdownListener{conns: make(chan net.Conn), done: make(chan struct{})}
	if !s.startListener(ln, "") {
		t.Fatal("listener not started")
	}
	server, client := net.Pipe()
	defer client.Close()
	ln.conns <- server
	awaitShutdown(t, monitor.registered)
	s.BeginShutdown()
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	awaitShutdown(t, done)
	awaitShutdown(t, monitor.unregistered)
	if s.startListener(ln, "") {
		t.Fatal("listener restarted during shutdown")
	}
}
