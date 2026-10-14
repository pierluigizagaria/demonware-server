package diagnostics

import (
	"bytes"
	"errors"
	"log"
	"net"
	"strings"
	"testing"
)

func TestGlobalRedactionDefault(t *testing.T) {
	testDiagnostics := &Diagnostics{RedactLogs: true, DumpRoot: "off"}
	if d := testDiagnostics.RunDumpDir("platform-redaction-test"); d != "" {
		t.Fatal("raw dumps enabled under global redaction")
	}
}

func TestRedactedLogsKeepNumbersAndHideSecrets(t *testing.T) {
	testDiagnostics := &Diagnostics{RedactLogs: true, DumpRoot: "off"}
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	testDiagnostics.DiagnosticLogf("task=%d ok=%t key=%X name=%q", 145, true, []byte{0xAB, 0xCD}, "player")
	if got := out.String(); !strings.Contains(got, "task=145 ok=true key=<redacted> name=<redacted>") || strings.Contains(got, "ABCD") || strings.Contains(got, "player") {
		t.Fatal("unexpected redaction: " + got)
	}
	out.Reset()
	type hit struct {
		id   uint64
		name string
	}
	addr := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 3074}
	err := &net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: errors.New("reset")}
	testDiagnostics.DiagnosticLogf("hits=%v map=%v from=%s err=%v", []hit{{1, "player"}}, map[string]int{"player": 1}, addr, err)
	if got := out.String(); strings.Contains(got, "player") || strings.Contains(got, "203.0.113.7") || !strings.Contains(got, "err=<*net.OpError>") {
		t.Fatal("structured values or error text leaked: " + got)
	}
}
