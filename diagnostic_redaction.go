package main

import (
	"log"
	"os"
	"strings"
)

var redactLogs = os.Getenv("NEXTENDO_REDACT_LOGS") != "0"

func diagnosticLogf(format string, args ...any) {
	if redactLogs {
		prefix, _, _ := strings.Cut(format, "%")
		log.Printf("%s[details redacted]", prefix)
		return
	}
	log.Printf(format, args...)
}
