package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeRejectsAmbiguousDocuments(t *testing.T) {
	var target struct {
		Value json.Number `json:"value"`
		Child any         `json:"child"`
	}
	if !Decode(`{"value":18446744073709551615,"child":{"name":"fixture"}}`, &target) || target.Value.String() != "18446744073709551615" {
		t.Fatal("valid document lost exact integer precision")
	}
	for _, raw := range []string{
		`{"value":1,"value":2}`, `{"child":{"name":1,"name":2}}`,
		`{"child":[{"name":1,"name":2}]}`, `{"unknown":1}`, `{} {}`,
		`{"value":`, `{"child":` + strings.Repeat("[", 14) + `0` + strings.Repeat("]", 14) + `}`,
	} {
		if Decode(raw, &target) {
			t.Fatal("ambiguous, unsupported or malformed document accepted")
		}
	}
}
