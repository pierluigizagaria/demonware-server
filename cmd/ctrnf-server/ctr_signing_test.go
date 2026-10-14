package main

import (
	bytes "bytes"
	rand "crypto/rand"
	rsa "crypto/rsa"
	sha256 "crypto/sha256"
	x509 "crypto/x509"
	base64 "encoding/base64"
	pem "encoding/pem"
	authpkg "github.com/pierluigizagaria/demonware-server/internal/auth"
	os "os"
	exec "os/exec"
	filepath "path/filepath"
	testing "testing"
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
	s, err := authpkg.LoadCTRAuthSigner(path)
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
		cmd := exec.Command(s.Openssl, "pkeyutl", "-verify", "-pubin", "-inkey", pubPath, "-sigfile", sigPath, "-pkeyopt", "digest:sha256", "-pkeyopt", "rsa_padding_mode:pss", "-pkeyopt", "rsa_pss_saltlen:0")
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
	if s, err := authpkg.LoadCTRAuthSigner(""); s != nil || err != nil {
		t.Fatal("unset configuration should disable signing")
	}
	if _, err := authpkg.LoadCTRAuthSigner(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing configured key accepted")
	}
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := authpkg.LoadCTRAuthSigner(path); err == nil {
		t.Fatal("invalid key accepted")
	}
}
