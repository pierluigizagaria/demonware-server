package main

import (
	"fmt"
	"log"
	"os"
)

var redactLogs = os.Getenv("NEXTENDO_REDACT_LOGS") != "0"

// diagnosticLogf keeps numbers, booleans and errors, which identify tasks,
// sizes and outcomes, but withholds strings and byte slices: those carry
// tickets, keys, tokens, request bodies, addresses and names.
func diagnosticLogf(format string, args ...any) {
	if redactLogs {
		safe := make([]any, len(args))
		for i, arg := range args {
			switch arg.(type) {
			case string, []byte, []string:
				safe[i] = redacted{}
			default:
				safe[i] = arg
			}
		}
		args = safe
	}
	log.Printf(format, args...)
}

type redacted struct{}

// Format prints the marker for every verb, including %X and %q.
func (redacted) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("<redacted>")) }
