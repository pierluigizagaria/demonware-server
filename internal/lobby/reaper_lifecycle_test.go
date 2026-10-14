package lobby

import (
	"testing"
	"time"
)

func TestRegistryReaperStopsAndCannotRestart(t *testing.T) {
	r := NewSessionRegistry()
	r.StartReaper()
	r.StartReaper()
	r.StopReaper()
	r.StopReaper()
	r.StartReaper()
	done := make(chan struct{})
	go func() { r.WaitReaper(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("registry reaper did not stop")
	}
}
