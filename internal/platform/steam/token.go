// Package steam provides verified Steam admission through a private native helper.
package steam

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var onlineID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,15}$`)

type Token struct {
	AccountID uint64
	OnlineID  string
	Hash      [32]byte
}

func ParseToken(value string) (Token, error) {
	var token Token
	fail := func() (Token, error) { return Token{}, fmt.Errorf("invalid Steam credential") }
	if len(value) > 4096 {
		return fail()
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != "osw1" {
		return fail()
	}
	id, err := decimalID(parts[1])
	if err != nil {
		return fail()
	}
	ticket, err := hex.DecodeString(parts[2])
	if err != nil || len(ticket) == 0 {
		return fail()
	}
	name, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil || !onlineID.Match(name) || base64.RawURLEncoding.EncodeToString(name) != parts[3] {
		return fail()
	}
	token.AccountID, token.OnlineID, token.Hash = id, string(name), sha256.Sum256(ticket)
	return token, nil
}

func decimalID(value string) (uint64, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
		return 0, fmt.Errorf("invalid identifier")
	}
	return id, nil
}
