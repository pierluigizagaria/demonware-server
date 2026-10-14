package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pierluigizagaria/demonware-server/internal/config"
	"github.com/pierluigizagaria/demonware-server/internal/ctr"
)

func TestCTRNewsConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name, text, problem string
	}{
		{"too long", strings.Repeat("x", 2048), "2047 UTF-8 bytes"},
		{"multibyte too long", strings.Repeat("é", 1024), "2047 UTF-8 bytes"},
		{"NUL", "a\x00b", "control characters"},
		{"newline", "a\nb", "control characters"},
		{"carriage return", "a\rb", "control characters"},
		{"tab", "a\tb", "control characters"},
		{"DEL", "a\x7fb", "control characters"},
		{"C1", "a\u0085b", "control characters"},
		{"line separator", "a\u2028b", "line separators"},
		{"paragraph separator", "a\u2029b", "line separators"},
		{"invalid UTF-8", string([]byte{0xff}), "valid UTF-8"},
		{"JSON too long", strings.Repeat("<", 679) + "xxx", "shorter than 4096 bytes"},
	} {
		for _, name := range []string{"CTR_NEWS", "CTR_NEWS_IT", "CTR_NEWS_PT_BR"} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				env := serverEnv()
				env[name] = tc.text
				_, err := config.Load(lookupFrom(env), []string{name}, true)
				if err == nil || !strings.Contains(err.Error(), name+":") || !strings.Contains(err.Error(), tc.problem) {
					t.Fatalf("invalid news not diagnosed: %v", err)
				}
			})
		}
	}
	_, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": "\n", "CTR_NEWS_IT": "\t"}), nil, true)
	for _, problem := range []string{"CTR_NEWS:", "CTR_NEWS_IT:", "SHADNET_ISSUER is required"} {
		if err == nil || !strings.Contains(err.Error(), problem) {
			t.Fatalf("startup did not collect every problem: %v", err)
		}
	}
}

func TestCTRNewsConfigAcceptsBoundaries(t *testing.T) {
	for _, text := range []string{"", "  Keep spaces  ", "Gare: العربية 日本語", strings.Repeat("x", 2047),
		strings.Repeat("é", 1023) + "x", strings.Repeat("<", 679) + "xx"} {
		c, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": text}), []string{"CTR_NEWS"}, false)
		if err != nil {
			t.Fatal(err)
		}
		msg := c.News.Message("en")
		if text == "" {
			if msg.ID != 0 || len(msg.Content) != 0 {
				t.Fatal("empty news produced a message")
			}
			continue
		}
		var content map[string]string
		if err := json.Unmarshal(msg.Content, &content); err != nil || content["content_long"] != text ||
			len(msg.Content) >= 4096 || msg.ID == 0 {
			t.Fatal("valid text did not round-trip")
		}
	}
}

func TestCTRNewsConfigKnownOverridesOnly(t *testing.T) {
	codes := strings.Fields("AR BG ZH CS DA NL EN FI FR DE EL HE HI HU IT JA KO NO PL PT RU ES MX SV TA TR PT_BR ES_MX")
	env := map[string]string{}
	var names []string
	for _, code := range codes {
		name := "CTR_NEWS_" + code
		env[name] = code
		names = append(names, name)
	}
	c, err := config.Load(lookupFrom(env), names, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.News.Languages) != len(codes) {
		t.Fatal("listed overrides were not all loaded")
	}
	for _, code := range codes {
		language := strings.ToLower(code)
		if parts := strings.SplitN(code, "_", 2); len(parts) == 2 {
			language = strings.ToLower(parts[0]) + "-" + parts[1]
		}
		var content map[string]string
		if err := json.Unmarshal(c.News.Message(language).Content, &content); err != nil || content["content_long"] != code {
			t.Fatalf("override %s did not map to %s", code, language)
		}
	}
	for _, name := range []string{"CTR_NEWS_XX", "CTR_NEWS_", "CTR_NEWS_EN_US", "CTR_NEWS_PT-BR", "CTR_NEWS_PT_BR_EXTRA"} {
		_, err := config.Load(lookupFrom(map[string]string{name: "test"}), []string{name}, false)
		if err == nil || !strings.Contains(err.Error(), name+" is not a setting") {
			t.Fatalf("unknown news variable accepted: %s", name)
		}
	}
}

func TestCTRNewsConfigAppliedPerInstance(t *testing.T) {
	request := newsFetch("ctr", "en", [2]uint32{1, 1})
	for _, tc := range []struct{ mode, text string }{{"0", "Normal news"}, {"1", "Other news"}, {"0", "Other news"}} {
		c, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": tc.text, "CTR_UNLOCK_ALL": tc.mode}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		l, wire := sharedPS4Lobby()
		l.ctr = &ctr.Services{News: c.News}
		r := newsReply(t, sharedTask(t, l, wire, request), 5, 0, 1)
		newsLocation(t, r, 1, 1)
		newsMessage(t, r, "en", tc.text)
		newsEnd(t, r)
	}
}

func TestCTRNewsMessageIDFollowsText(t *testing.T) {
	load := func(text string) config.News {
		t.Helper()
		c, err := config.Load(lookupFrom(map[string]string{"CTR_NEWS": text, "CTR_NEWS_IT": text}), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		return c.News
	}
	first, again, changed := load("Welcome"), load("Welcome"), load("Welcome!")
	id := first.Message("en").ID
	if id == 0 || again.Message("en").ID != id || first.Message("it").ID != id ||
		changed.Message("en").ID == 0 || changed.Message("en").ID == id ||
		!bytes.Equal(first.Message("en").Content, again.Message("en").Content) {
		t.Fatal("message ID must be stable for the same text and change with new text")
	}
}
