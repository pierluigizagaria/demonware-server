package main

import (
	hex "encoding/hex"
	fmt "fmt"
	demonwarepkg "github.com/pierluigizagaria/demonware-server/internal/demonware"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
)

// Only these 12 snapshots differ from the immutable Phase 0 JSON. Service 21
// now follows the ordinary unknown-service path, with no registry mutation.
// Every other snapshot continues to compare against its original bytes.
func architectureDiabloDisconnection(name string, baseline architectureSnapshot) architectureSnapshot {
	for _, task := range []byte{1, 19} {
		for _, args := range []string{"empty", "malformed"} {
			for _, fail := range []int{0, 1, 2} {
				if name != fmt.Sprintf("021-%03d-%s/write-fail-%d", task, args, fail) {
					continue
				}
				baseline.Frames = append([]architectureFrame(nil), baseline.Frames...)
				baseline.Frames[0].Payload = hex.EncodeToString(demonwarepkg.TaskReply(1, task, 0, nil))
				baseline.Sessions = 0
				return baseline
			}
		}
	}
	return baseline
}

func TestCTRPublisherFilesExcludeDiabloOutputs(t *testing.T) {
	l, w, _ := architectureBaseline(t)
	for _, name := range []string{"Config.txt", "Seasons.txt", "seasons_config.txt", "Blacklist.txt", "blacklist_config.txt", "challengerift_config.dat", "challengerift_42.dat", "CTR-fixture.dat"} {
		if err := os.WriteFile(filepath.Join(l.pubDir, name), []byte("synthetic file"), 0600); err != nil {
			t.Fatal(err)
		}
		q := demonwarepkg.NewWriter([]byte{10, 3, 21})
		q.String("fixture")
		q.String(strings.ToUpper(name))
		p := architectureReply(t, architectureCall(t, l, w, q.Bytes()), 21, 0)
		if name == "CTR-fixture.dat" {
			r := demonwarepkg.NewReader(p[26:])
			data, err := r.Blob()
			if err != nil || string(data) != "synthetic file" {
				t.Fatal("CTR physical file not served")
			}
		} else if len(p) != 21 {
			t.Fatalf("Diablo output still reachable: %s", name)
		}
	}
}

func TestCTRTableDoesNotReachDiablo(t *testing.T) {
	l, w, _ := architectureBaseline(t)
	l.fixtureTable()
	if _, ok := l.Table.Handlers[21]; ok {
		t.Fatal("Diablo adapter wired")
	}
	p := architectureReply(t, architectureCall(t, l, w, []byte{21, 3, 19}), 19, 0)
	if len(p) != 21 || len(l.registry.Sessions) != 0 {
		t.Fatal("Diablo reservation reached from CTR")
	}
}
