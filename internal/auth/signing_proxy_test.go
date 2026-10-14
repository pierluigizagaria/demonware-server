package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCTRAuthSigning(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl unavailable")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "private.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadCTRSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"auth_task":"79","code":"700"}`)
	encoded, err := s.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPath, sigPath := filepath.Join(dir, "public.pem"), filepath.Join(dir, "sig.bin")
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, sig, 0600); err != nil {
		t.Fatal(err)
	}
	verify := func(data []byte) error {
		hash := sha256.Sum256(data)
		cmd := exec.Command(s.OpenSSL, "pkeyutl", "-verify", "-pubin", "-inkey", pubPath, "-sigfile", sigPath, "-pkeyopt", "digest:sha256", "-pkeyopt", "rsa_padding_mode:pss", "-pkeyopt", "rsa_pss_saltlen:0")
		cmd.Stdin = bytes.NewReader(hash[:])
		return cmd.Run()
	}
	if err := verify(body); err != nil {
		t.Fatalf("exact zero-salt verification: %v", err)
	}
	if err := verify(append(body, ' ')); err == nil {
		t.Fatal("modified response accepted")
	}
}

func TestCTRAuthSigningConfiguration(t *testing.T) {
	if s, err := LoadCTRSigner(""); s != nil || err != nil {
		t.Fatal("unset configuration should disable signing")
	}
	if _, err := LoadCTRSigner(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing configured key accepted")
	}
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCTRSigner(path); err == nil {
		t.Fatal("invalid key accepted")
	}
}

func acceptOne(t *testing.T, send string) (net.Conn, []byte) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	ln := NewProxyListener(inner)

	go func() {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(send))
		time.Sleep(200 * time.Millisecond)
	}()

	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	return c, buf
}

func TestProxyHeaderSetsRemoteAddr(t *testing.T) {
	c, rest := acceptOne(t, "PROXY TCP4 192.0.2.10 192.0.2.1 51000 443\r\nhello")
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || addr.IP.String() != "192.0.2.10" || addr.Port != 51000 {
		t.Fatalf("RemoteAddr = %v", c.RemoteAddr())
	}
	if string(rest) != "hello" {
		t.Fatalf("payload after header = %q", rest)
	}
}

func TestNoProxyHeaderPassesThrough(t *testing.T) {
	c, rest := acceptOne(t, "\x16\x03\x01hello")
	defer c.Close()
	if addr, ok := c.RemoteAddr().(*net.TCPAddr); !ok || !addr.IP.IsLoopback() {
		t.Fatalf("RemoteAddr = %v", c.RemoteAddr())
	}
	if string(rest) != "\x16\x03\x01he" {
		t.Fatalf("payload = %q", rest)
	}
}
