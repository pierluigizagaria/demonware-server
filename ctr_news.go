package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/NextendoNetwork/nextendo-demonware/internal/demonware"
)

const (
	svcMarketingComms           = 104
	taskReportMessagesViewed    = 4
	taskGetMessagesForLocations = 5
	maxNewsEntries              = 8
)

type ctrNewsMessage struct {
	id      uint64
	content []byte
}

type ctrNewsConfig struct {
	fallback  ctrNewsMessage
	languages map[string]ctrNewsMessage
}

var ctrNews ctrNewsConfig

func newCTRNewsMessage(text string) (ctrNewsMessage, error) {
	if !utf8.ValidString(text) {
		return ctrNewsMessage{}, fmt.Errorf("expected valid UTF-8")
	}
	for _, ch := range text {
		if unicode.IsControl(ch) || ch == '\u2028' || ch == '\u2029' {
			return ctrNewsMessage{}, fmt.Errorf("expected one line without control characters or line separators")
		}
	}
	if len(text) > 2047 {
		return ctrNewsMessage{}, fmt.Errorf("content_long exceeds 2047 UTF-8 bytes")
	}
	if text == "" {
		return ctrNewsMessage{}, nil
	}
	content, err := json.Marshal(map[string]string{"content_long": text})
	if err != nil {
		return ctrNewsMessage{}, err
	}
	if len(content) >= 4096 {
		return ctrNewsMessage{}, fmt.Errorf("encoded JSON content must be shorter than 4096 bytes")
	}
	sum := sha256.Sum256([]byte(text))
	id := binary.BigEndian.Uint64(sum[:8])
	if id == 0 {
		id = 1
	}
	return ctrNewsMessage{id: id, content: content}, nil
}

func (c ctrNewsConfig) message(language string) ctrNewsMessage {
	if msg, ok := c.languages[language]; ok {
		return msg
	}
	return c.fallback
}

// CUSA13795 01.21: 0x90e90 builds 104/5; 0x90170 and 0x91700 read
// locations and messages. 0xcda540 consumes only content_long from the JSON.
func (l *lobbyConn) onMarketingComms(task byte, r *demonware.Reader) []byte {
	if l.player == nil || l.player.PID == 0 || !isCTRTitle(l.player.Title) {
		return taskReply(task, errUnhandled, nil)
	}
	key, err := r.String()
	if err != nil || len(key) > 15 {
		return taskReply(task, errUnhandled, nil)
	}
	switch task {
	case taskGetMessagesForLocations:
		language, err := r.String()
		if err != nil || len(language) > 19 {
			return taskReply(task, errUnhandled, nil)
		}
		count, err := r.Uint32()
		if err != nil || count == 0 || count > maxNewsEntries {
			return taskReply(task, errUnhandled, nil)
		}
		locations := make([]struct{ location, capacity uint32 }, count)
		for i := range locations {
			location, e1 := r.Uint32()
			capacity, e2 := r.Uint32()
			if e1 != nil || e2 != nil {
				return taskReply(task, errUnhandled, nil)
			}
			locations[i].location, locations[i].capacity = location, capacity
		}
		if len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
			return taskReply(task, errUnhandled, nil)
		}
		msg := ctrNews.message(language)
		return taskReply(task, 0, func(w *demonware.Writer) uint32 {
			for _, loc := range locations {
				w.Uint32(loc.location)
				if loc.location != 1 || loc.capacity == 0 || len(msg.content) == 0 {
					w.Uint32(0)
					continue
				}
				w.Uint32(1)
				w.Uint64(msg.id)
				w.String(language)
				w.Blob(msg.content)
				w.Blob([]byte("{}"))
			}
			return count
		})
	case taskReportMessagesViewed:
		count, err := r.Uint32()
		if err != nil || count > maxNewsEntries {
			return taskReply(task, errUnhandled, nil)
		}
		ids := make([]uint64, count)
		for i := range ids {
			language, e1 := r.String()
			id, e2 := r.Uint64()
			_, e3 := r.Uint32()
			if e1 != nil || len(language) > 19 || e2 != nil || e3 != nil {
				return taskReply(task, errUnhandled, nil)
			}
			ids[i] = id
		}
		if len(r.Remaining()) != 1 || r.Remaining()[0] != 0 {
			return taskReply(task, errUnhandled, nil)
		}
		l.logf("CTR news viewed count=%d", count)
		for _, id := range ids {
			l.logf("CTR news viewed id=%d", id)
		}
		return taskReply(task, 0, nil)
	}
	return taskReply(task, errUnhandled, nil)
}
