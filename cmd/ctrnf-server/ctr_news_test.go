package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/demonware"
	"github.com/pierluigizagaria/demonware-server/internal/lobby"
	"github.com/pierluigizagaria/demonware-server/internal/platform"
	"github.com/pierluigizagaria/demonware-server/internal/platform/ps4"
)

func setNews(t *testing.T, env map[string]string) {
	t.Helper()
	c, err := config.Load(lookupFrom(env), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	old := testCTR.News
	testCTR.News = c.News
	t.Cleanup(func() { testCTR.News = old })
}

func newsFetch(key, language string, locations ...[2]uint32) []byte {
	w := demonware.NewWriter([]byte{104, demonware.TagUint8, 5})
	w.String(key)
	w.String(language)
	w.Uint32(uint32(len(locations)))
	for _, loc := range locations {
		w.Uint32(loc[0])
		w.Uint32(loc[1])
	}
	return append(w.Bytes(), 0)
}

func newsViewed(key, language string, ids ...uint64) []byte {
	w := demonware.NewWriter([]byte{104, demonware.TagUint8, 4})
	w.String(key)
	w.Uint32(uint32(len(ids)))
	for _, id := range ids {
		w.String(language)
		w.Uint64(id)
		w.Uint32(1)
	}
	return append(w.Bytes(), 0)
}

func newsReply(t *testing.T, reply []byte, task byte, code, count uint32) *demonware.Reader {
	t.Helper()
	r := demonware.NewReader(reply)
	if tx, err := r.Uint64(); err != nil || tx == 0 {
		t.Fatalf("transaction = %d, %v", tx, err)
	}
	if got, err := r.Uint32(); err != nil || got != code {
		t.Fatalf("error = %d, %v; want %d", got, err, code)
	}
	if code != 0 {
		newsEnd(t, r)
		return r
	}
	if got, err := r.Uint8(); err != nil || got != task {
		t.Fatalf("task = %d, %v; want %d", got, err, task)
	}
	if got, err := r.Uint32(); err != nil || got != count {
		t.Fatalf("result count = %d, %v; want %d", got, err, count)
	}
	if count != 0 {
		if got, err := r.Uint32(); err != nil || got != count {
			t.Fatalf("total = %d, %v; want %d", got, err, count)
		}
	} else {
		newsEnd(t, r)
	}
	return r
}

func newsEnd(t *testing.T, r *demonware.Reader) {
	t.Helper()
	if len(r.Remaining()) != 0 {
		t.Fatalf("unexpected trailing reply bytes: %d", len(r.Remaining()))
	}
}

func newsLocation(t *testing.T, r *demonware.Reader, location, count uint32) {
	t.Helper()
	if got, err := r.Uint32(); err != nil || got != location {
		t.Fatalf("location = %d, %v; want %d", got, err, location)
	}
	if got, err := r.Uint32(); err != nil || got != count {
		t.Fatalf("message count = %d, %v; want %d", got, err, count)
	}
}

func newsMessage(t *testing.T, r *demonware.Reader, language, text string) uint64 {
	t.Helper()
	id, err := r.Uint64()
	if err != nil || id == 0 {
		t.Fatalf("message ID = %d, %v", id, err)
	}
	if got, err := r.String(); err != nil || got != language || len(got) > 19 {
		t.Fatalf("language = %q, %v; want %q", got, err, language)
	}
	content, err := r.Blob()
	if err != nil || len(content) >= 4096 {
		t.Fatalf("content length = %d, %v", len(content), err)
	}
	var object map[string]string
	if err := json.Unmarshal(content, &object); err != nil || len(object) != 1 || object["content_long"] != text {
		t.Fatalf("content_long did not round-trip: %v", err)
	}
	if metadata, err := r.Blob(); err != nil || string(metadata) != "{}" {
		t.Fatalf("metadata = %q, %v", metadata, err)
	}
	return id
}

func TestCTRNewsGameRequestAndReply(t *testing.T) {
	text := "Benvenuti! Caffè, \"gare\" e https://example.net/?a=1&b=2"
	setNews(t, map[string]string{"CTR_NEWS": text})
	l, wire := sharedPS4Lobby()
	// Synthetic wire fixture from CUSA13795 01.21: ctr, en, one location,
	// location 1, capacity 1, terminator (28 bytes including the header).
	request, err := hex.DecodeString("680305106374720010656e0008010000000801000000080100000000")
	if err != nil || len(request) != 28 {
		t.Fatal("invalid game request fixture")
	}
	r := newsReply(t, sharedTask(t, l, wire, request), 5, 0, 1)
	newsLocation(t, r, 1, 1)
	id := newsMessage(t, r, "en", text)
	newsEnd(t, r)
	r = newsReply(t, sharedTask(t, l, wire, newsViewed("octane_psn", "en", id)), 4, 0, 0)
	newsEnd(t, r)
	// Reporting a view must not consume the instance's message.
	r = newsReply(t, sharedTask(t, l, wire, request), 5, 0, 1)
	newsLocation(t, r, 1, 1)
	if got := newsMessage(t, r, "en", text); got != id {
		t.Fatal("view report changed the message")
	}
	newsEnd(t, r)
	if l.AfterReplyActive {
		t.Fatal("news scheduled a push")
	}
}

func TestCTRNewsLanguageOverrideAndFallback(t *testing.T) {
	setNews(t, map[string]string{
		"CTR_NEWS": "Default", "CTR_NEWS_EN": "English", "CTR_NEWS_IT": "Italiano",
		"CTR_NEWS_PT": "Português", "CTR_NEWS_PT_BR": "Brasil", "CTR_NEWS_ES_MX": "México",
		"CTR_NEWS_FR": "",
	})
	l, wire := sharedPS4Lobby()
	for language, want := range map[string]string{
		"en": "English", "it": "Italiano", "pt": "Português", "pt-BR": "Brasil", "es-MX": "México",
		"fr": "Default", "de": "Default", "xx": "Default", "pt-br": "Default", "es": "Default",
	} {
		t.Run(language, func(t *testing.T) {
			r := newsReply(t, sharedTask(t, l, wire, newsFetch("ctr", language, [2]uint32{1, 1})), 5, 0, 1)
			newsLocation(t, r, 1, 1)
			newsMessage(t, r, language, want)
			newsEnd(t, r)
		})
	}
}

func TestCTRNewsEmptyAndLocationCapacity(t *testing.T) {
	for _, text := range []string{"", "Welcome"} {
		t.Run(text, func(t *testing.T) {
			setNews(t, map[string]string{"CTR_NEWS": text})
			l, wire := sharedPS4Lobby()
			locations := [][2]uint32{{9, 1}, {1, 0}, {1, 1}, {1, 2}, {0, 1}, {1, ^uint32(0)}, {2, 0}, {1, 1}}
			r := newsReply(t, sharedTask(t, l, wire, newsFetch("ctr", "en", locations...)), 5, 0, 8)
			for _, loc := range locations {
				count := uint32(0)
				if text != "" && loc[0] == 1 && loc[1] > 0 {
					count = 1
				}
				newsLocation(t, r, loc[0], count)
				if count != 0 {
					newsMessage(t, r, "en", text)
				}
			}
			newsEnd(t, r)
		})
	}
	setNews(t, map[string]string{"CTR_NEWS_IT": "Solo italiano"})
	l, wire := sharedPS4Lobby()
	for _, language := range []string{"en", "it"} {
		r := newsReply(t, sharedTask(t, l, wire, newsFetch("ctr", language, [2]uint32{1, 1})), 5, 0, 1)
		if language == "en" {
			newsLocation(t, r, 1, 0)
		} else {
			newsLocation(t, r, 1, 1)
			newsMessage(t, r, language, "Solo italiano")
		}
		newsEnd(t, r)
	}
}

func TestCTRNewsRefusesMalformedRequests(t *testing.T) {
	setNews(t, map[string]string{"CTR_NEWS": "Welcome"})
	l, wire := sharedPS4Lobby()
	fetch := newsFetch("ctr", "en", [2]uint32{1, 1})
	viewed := newsViewed("octane_psn", "en", 123)
	for _, request := range [][]byte{fetch, viewed} {
		for n := 3; n < len(request); n++ {
			newsReply(t, sharedTask(t, l, wire, request[:n]), request[2], lobby.ErrUnhandled, 0)
		}
		for _, suffix := range []byte{0, 1} {
			newsReply(t, sharedTask(t, l, wire, append(bytes.Clone(request), suffix)), request[2], lobby.ErrUnhandled, 0)
		}
		bad := bytes.Clone(request)
		bad[len(bad)-1] = 1
		newsReply(t, sharedTask(t, l, wire, bad), request[2], lobby.ErrUnhandled, 0)
		bad = bytes.Clone(request)
		bad[1] = demonware.TagBool
		newsReply(t, sharedTask(t, l, wire, bad), request[2], lobby.ErrUnhandled, 0)
	}
	// Wrong tags for every argument, including the u64 viewed message ID.
	for _, tc := range []struct {
		request []byte
		offsets []int
	}{{fetch, []int{3, 8, 12, 17, 22}}, {viewed, []int{3, 15, 20, 24, 33}}} {
		for _, offset := range tc.offsets {
			bad := bytes.Clone(tc.request)
			bad[offset] = demonware.TagBool
			newsReply(t, sharedTask(t, l, wire, bad), bad[2], lobby.ErrUnhandled, 0)
		}
	}
	badRequests := [][]byte{
		newsFetch(strings.Repeat("k", 16), "en", [2]uint32{1, 1}),
		newsFetch("ctr", strings.Repeat("l", 20), [2]uint32{1, 1}),
		newsFetch("ctr", strings.Repeat("é", 10), [2]uint32{1, 1}),
		newsFetch("ctr", "en"),
		newsFetch("ctr", "en", make([][2]uint32, 9)...),
		newsViewed(strings.Repeat("k", 16), "en", 1),
		newsViewed("octane_psn", strings.Repeat("l", 20), 1),
		newsViewed("octane_psn", "en", make([]uint64, 9)...),
	}
	for _, request := range [][]byte{fetch, viewed} {
		bad := bytes.Clone(request)
		offset := 12
		if request[2] == 4 {
			offset = 15
		}
		copy(bad[offset+1:offset+5], []byte{255, 255, 255, 255})
		badRequests = append(badRequests, bad)
	}
	for _, bad := range badRequests {
		newsReply(t, sharedTask(t, l, wire, bad), bad[2], lobby.ErrUnhandled, 0)
	}
	for _, task := range []byte{0, 1, 6, 7, 255} {
		bad := bytes.Clone(fetch)
		bad[2] = task
		newsReply(t, sharedTask(t, l, wire, bad), task, lobby.ErrUnhandled, 0)
	}
}

func TestCTRNewsRequestLimitsAccepted(t *testing.T) {
	setNews(t, map[string]string{"CTR_NEWS": "Welcome"})
	l, wire := sharedPS4Lobby()
	language := strings.Repeat("l", 19)
	request := newsFetch(strings.Repeat("k", 15), language, [2]uint32{1, 1})
	r := newsReply(t, sharedTask(t, l, wire, request), 5, 0, 1)
	newsLocation(t, r, 1, 1)
	newsMessage(t, r, language, "Welcome")
	newsEnd(t, r)
	for _, count := range []int{0, 8} {
		request = newsViewed(strings.Repeat("k", 15), language, make([]uint64, count)...)
		newsReply(t, sharedTask(t, l, wire, request), 4, 0, 0)
	}
}

func TestCTRNewsRequiresAdmittedPS4Player(t *testing.T) {
	setNews(t, map[string]string{"CTR_NEWS": "Welcome"})
	l, wire := sharedPS4Lobby()
	for _, player := range []*platform.PlayerID{nil, {Title: ps4.CTRTitle}, {PID: 17, Title: 5775}, {PID: 17, Title: 5745}} {
		l.Player = player
		for _, request := range [][]byte{newsFetch("ctr", "en", [2]uint32{1, 1}), newsViewed("octane_psn", "en", 1)} {
			newsReply(t, sharedTask(t, l, wire, request), request[2], lobby.ErrUnhandled, 0)
		}
	}
}

func TestCTRNewsViewedLogsOnlyCountsAndIDs(t *testing.T) {
	var out bytes.Buffer
	oldWriter, oldRedact := log.Writer(), testDiagnostics.RedactLogs
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(oldWriter); testDiagnostics.RedactLogs = oldRedact })
	l, wire := sharedPS4Lobby()
	for _, redact := range []bool{true, false} {
		testDiagnostics.RedactLogs = redact
		out.Reset()
		request := newsViewed("private-key", "private-language", 123, 456)
		newsReply(t, sharedTask(t, l, wire, request), 4, 0, 0)
		got := out.String()
		if !strings.Contains(got, "count=2") || !strings.Contains(got, "id=123") || !strings.Contains(got, "id=456") ||
			strings.Contains(got, "private") || strings.Contains(got, "fixture") {
			t.Fatal("viewed report did not log only counts and IDs")
		}
		out.Reset()
		newsReply(t, sharedTask(t, l, wire, request[:len(request)-1]), 4, lobby.ErrUnhandled, 0)
		if out.Len() != 0 {
			t.Fatal("malformed report logged views")
		}
	}
}
