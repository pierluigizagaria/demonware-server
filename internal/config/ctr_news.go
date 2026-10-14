package config

import (
	sha256 "crypto/sha256"
	binary "encoding/binary"
	json "encoding/json"
	fmt "fmt"
	unicode "unicode"
	utf8 "unicode/utf8"
)

type CtrNewsMessage struct {
	Id      uint64
	Content []byte
}

type CtrNewsConfig struct {
	Fallback  CtrNewsMessage
	Languages map[string]CtrNewsMessage
}

func newCTRNewsMessage(text string) (CtrNewsMessage, error) {
	if !utf8.ValidString(text) {
		return CtrNewsMessage{}, fmt.Errorf("expected valid UTF-8")
	}
	for _, ch := range text {
		if unicode.IsControl(ch) || ch == '\u2028' || ch == '\u2029' {
			return CtrNewsMessage{}, fmt.Errorf("expected one line without control characters or line separators")
		}
	}
	if len(text) > 2047 {
		return CtrNewsMessage{}, fmt.Errorf("content_long exceeds 2047 UTF-8 bytes")
	}
	if text == "" {
		return CtrNewsMessage{}, nil
	}
	content, err := json.Marshal(map[string]string{"content_long": text})
	if err != nil {
		return CtrNewsMessage{}, err
	}
	if len(content) >= 4096 {
		return CtrNewsMessage{}, fmt.Errorf("encoded JSON content must be shorter than 4096 bytes")
	}
	sum := sha256.Sum256([]byte(text))
	id := binary.BigEndian.Uint64(sum[:8])
	if id == 0 {
		id = 1
	}
	return CtrNewsMessage{Id: id, Content: content}, nil
}

func (c CtrNewsConfig) Message(language string) CtrNewsMessage {
	if msg, ok := c.Languages[language]; ok {
		return msg
	}
	return c.Fallback
}
