package config

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"
)

type NewsMessage struct {
	ID      uint64
	Content []byte
}

type News struct {
	Fallback  NewsMessage
	Languages map[string]NewsMessage
}

func newNewsMessage(text string) (NewsMessage, error) {
	if !utf8.ValidString(text) {
		return NewsMessage{}, fmt.Errorf("expected valid UTF-8")
	}
	for _, ch := range text {
		if unicode.IsControl(ch) || ch == '\u2028' || ch == '\u2029' {
			return NewsMessage{}, fmt.Errorf("expected one line without control characters or line separators")
		}
	}
	if len(text) > 2047 {
		return NewsMessage{}, fmt.Errorf("content_long exceeds 2047 UTF-8 bytes")
	}
	if text == "" {
		return NewsMessage{}, nil
	}
	content, err := json.Marshal(map[string]string{"content_long": text})
	if err != nil {
		return NewsMessage{}, err
	}
	if len(content) >= 4096 {
		return NewsMessage{}, fmt.Errorf("encoded JSON content must be shorter than 4096 bytes")
	}
	sum := sha256.Sum256([]byte(text))
	id := binary.BigEndian.Uint64(sum[:8])
	if id == 0 {
		id = 1
	}
	return NewsMessage{ID: id, Content: content}, nil
}

func (c News) Message(language string) NewsMessage {
	if msg, ok := c.Languages[language]; ok {
		return msg
	}
	return c.Fallback
}
