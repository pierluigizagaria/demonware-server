package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Issued tickets and their identities are named by the ticket's 8-byte
// session tag, so admission reads one file instead of scanning the directory.
func ticketKey(ticket []byte) string { return hex.EncodeToString(ticket[33:41]) }

func accountFile(dir string, pid uint64) string {
	return filepath.Join(dir, fmt.Sprintf("account_%d.json", pid))
}

// persistTicket stores the identity before the ticket, so a lobby that finds
// the ticket can always resolve who it was issued to. The account file keeps
// the latest identity for name lookups after the ticket files are pruned.
func persistTicket(dir string, ticket []byte, id playerID) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return err
	}
	key := ticketKey(ticket)
	if err := os.WriteFile(filepath.Join(dir, "id_"+key+".json"), raw, 0o600); err != nil {
		return err
	}
	if id.PID != 0 && id.Username != "" {
		if err := writeFileAtomic(accountFile(dir, id.PID), raw); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, key+".tkt"), ticket, 0o600); err != nil {
		return err
	}
	rememberIdentity(dir, id, time.Now())
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".account-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// issuedTicketPlayer admits a ticket only if this server issued exactly these
// bytes within window and its stored identity matches the ticket's account.
func issuedTicketPlayer(dir string, ticket []byte, now time.Time, window time.Duration) (*playerID, bool) {
	if len(ticket) != 128 || binary.LittleEndian.Uint32(ticket) != ticketMagic {
		return nil, false
	}
	issued := time.Unix(int64(binary.LittleEndian.Uint32(ticket[13:17])), 0)
	if issued.After(now) || now.Sub(issued) >= window {
		return nil, false
	}
	stored, err := os.ReadFile(filepath.Join(dir, ticketKey(ticket)+".tkt"))
	if err != nil || !bytes.Equal(stored, ticket) {
		return nil, false
	}
	id, ok := storedIdentity(dir, ticket)
	if !ok || id.PID != binary.LittleEndian.Uint64(ticket[17:25]) {
		return nil, false
	}
	return id, true
}

// ticketWasIssued serves the original admission, whose tickets are named by
// issue time and are found by content.
func ticketWasIssued(dir string, ticket []byte) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.tkt"))
	for _, file := range files {
		if stored, err := os.ReadFile(file); err == nil && bytes.Equal(stored, ticket) {
			return true
		}
	}
	return false
}

// storedIdentity reads the identity recorded at issuance; it is not admission.
func storedIdentity(dir string, ticket []byte) (*playerID, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "id_"+ticketKey(ticket)+".json"))
	var id playerID
	if err != nil || json.Unmarshal(raw, &id) != nil || id.PID == 0 {
		return nil, false
	}
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}

// pruneIssuedTickets removes ticket and per-ticket identity files that can no
// longer admit anyone. Account files, which resolve known players, are kept.
func pruneIssuedTickets(dir string, window time.Duration, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-window - time.Minute)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".tkt") && !(strings.HasPrefix(name, "id_") && strings.HasSuffix(name, ".json")) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func pruneIssuedTicketsEvery(dir string, window time.Duration) {
	for {
		pruneIssuedTickets(dir, window, time.Now())
		time.Sleep(time.Minute)
	}
}

// Identities already issued a ticket, indexed in memory by lower-case name
// per session directory. The index is loaded once from disk and updated at
// issuance, so name lookups never scan the directory.
var (
	knownMu   sync.Mutex
	knownDirs = map[string]map[string]knownIdentity{}
)

type knownIdentity struct {
	id playerID
	at time.Time
}

// PS4 keeps account files; the original admission keeps one identity file per
// ticket.
func knownIndexLocked(dir string) map[string]knownIdentity {
	if idx := knownDirs[dir]; idx != nil {
		return idx
	}
	idx := map[string]knownIdentity{}
	for _, pattern := range []string{"account_*.json", "id_*.json"} {
		files, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, file := range files {
			info, err := os.Stat(file)
			raw, err2 := os.ReadFile(file)
			var id playerID
			if err != nil || err2 != nil || json.Unmarshal(raw, &id) != nil {
				continue
			}
			addKnownIdentity(idx, id, info.ModTime())
		}
	}
	knownDirs[dir] = idx
	return idx
}

// The most recently recorded identity wins for each name.
func addKnownIdentity(idx map[string]knownIdentity, id playerID, at time.Time) {
	if id.PID == 0 || id.Username == "" {
		return
	}
	key := strings.ToLower(id.Username)
	if old, ok := idx[key]; !ok || at.After(old.at) {
		id.Title = 0
		idx[key] = knownIdentity{id: id, at: at}
	}
}

func rememberIdentity(dir string, id playerID, at time.Time) {
	knownMu.Lock()
	defer knownMu.Unlock()
	addKnownIdentity(knownIndexLocked(dir), id, at)
}

// knownIdentities returns the recorded identities for the requested names,
// keyed by lower-case name.
func knownIdentities(dir string, names []string) map[string]*playerID {
	knownMu.Lock()
	defer knownMu.Unlock()
	idx := knownIndexLocked(dir)
	out := map[string]*playerID{}
	for _, name := range names {
		key := strings.ToLower(name)
		if k, ok := idx[key]; ok {
			id := k.id
			out[key] = &id
		}
	}
	return out
}
