package local

import (
	"strings"
	"sync"
	"testing"
)

func TestRenamePreservesAdmissionAndIdentity(t *testing.T) {
	o := ownerFixture()
	p, _ := New(o)
	original := p.Current()
	trust := p.TrustID()
	for _, name := range []string{"", " leading", "trailing ", "bad\nname", strings.Repeat("x", 17), string([]byte{0xff})} {
		if _, err := p.Rename(o.SteamID, name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	if _, err := p.Rename("76561198000000002", "Other"); err == nil {
		t.Fatal("owner changed")
	}
	if _, err := p.Rename(o.SteamID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	id, err := p.Authenticate(envelope(o.Token))
	if err != nil || id.Username != "Renamed" || id.PID != original.PID || id.Sub != original.Sub || p.TrustID() != trust {
		t.Fatal("rename changed authority")
	}
	if original.Username != o.OnlineID {
		t.Fatal("existing session identity mutated")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if _, err := p.Rename(o.SteamID, "Renamed"); err != nil {
					t.Error(err)
				}
				id, err := p.Authenticate(envelope(o.Token))
				if err != nil || id.Username != "Renamed" || id.PID != original.PID {
					t.Error("concurrent admission changed")
				}
			}
		}()
	}
	workers.Wait()
}
