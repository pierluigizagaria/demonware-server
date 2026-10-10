// Package strictjson rejects ambiguous JSON in admission and protocol documents.
package strictjson

import (
	"encoding/json"
	"io"
	"strings"
)

// Reject duplicate keys at any depth, trailing values and excessive nesting.
func Decode(raw string, out any) bool {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 12 {
			return false
		}
		t, err := d.Token()
		if err != nil {
			return false
		}
		if c, ok := t.(json.Delim); ok {
			switch c {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					key, ok := k.(string)
					if err != nil || !ok || seen[key] {
						return false
					}
					seen[key] = true
					if !value(depth + 1) {
						return false
					}
				}
			case '[':
				for d.More() {
					if !value(depth + 1) {
						return false
					}
				}
			default:
				return false
			}
			end, err := d.Token()
			return err == nil && ((c == '{' && end == json.Delim('}')) || (c == '[' && end == json.Delim(']')))
		}
		return true
	}
	if !value(0) {
		return false
	}
	if _, err := d.Token(); err != io.EOF {
		return false
	}
	d = json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(out) == nil
}
