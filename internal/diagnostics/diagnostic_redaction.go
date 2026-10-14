package diagnostics

import (
	fmt "fmt"
	log "log"
	os "os"
	filepath "path/filepath"
	reflect "reflect"
)

// NEXTENDO_REDACT_LOGS, on unless set to 0 or false (config.go).
type Diagnostics struct {
	RedactLogs         bool
	Verbose            bool
	DumpRoot, RunStamp string
}

// diagnosticLogf keeps numbers and booleans, which identify tasks, sizes and
// outcomes, and the type of an error. Everything else is withheld: strings,
// byte slices, structs, maps, addresses and error texts can carry tickets,
// keys, tokens, request bodies, addresses and names.
func (d *Diagnostics) DiagnosticLogf(format string, args ...any) {
	if d.RedactLogs {
		safe := make([]any, len(args))
		for i, arg := range args {
			safe[i] = redactArg(arg)
		}
		args = safe
	}
	log.Printf(format, args...)
}

func redactArg(arg any) any {
	if err, ok := arg.(error); ok {
		return redacted{fmt.Sprintf("<%T>", err)}
	}
	switch reflect.ValueOf(arg).Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return arg
	}
	return redacted{"<redacted>"}
}

type redacted struct{ marker string }

// Format prints the marker for every verb, including %X and %q.
func (r redacted) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(r.marker)) }

// runDumpDir returns this run's capture directory for one component, "" when
// captures are off.
func (d *Diagnostics) RunDumpDir(component string) string {
	if d.RedactLogs || d.DumpRoot == "off" || d.DumpRoot == "0" {
		return ""
	}
	dir := filepath.Join(d.DumpRoot, d.RunStamp, component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		d.DiagnosticLogf("[diagnostics] dumps %s: %v", dir, err)
		return ""
	}
	return dir
}
