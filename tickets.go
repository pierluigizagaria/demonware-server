package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// persistTicket stores the identity before the ticket, so a lobby that finds
// the ticket can always resolve who it was issued to.
func persistTicket(dir string, ticket []byte, id playerID) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("id_%x.json", ticket[33:41])), raw, 0o600); err != nil {
		return err
	}
	name := fmt.Sprintf("%d_%x.tkt", time.Now().Unix(), ticket[33:37])
	return os.WriteFile(filepath.Join(dir, name), ticket, 0o600)
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
	if !ticketWasIssued(dir, ticket) {
		return nil, false
	}
	id, ok := storedIdentity(dir, ticket)
	if !ok || id.PID != binary.LittleEndian.Uint64(ticket[17:25]) {
		return nil, false
	}
	return id, true
}

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
	raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("id_%x.json", ticket[33:41])))
	var id playerID
	if err != nil || json.Unmarshal(raw, &id) != nil || id.PID == 0 {
		return nil, false
	}
	id.Title = binary.LittleEndian.Uint32(ticket[5:9])
	return &id, true
}
