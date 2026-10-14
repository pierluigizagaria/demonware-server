package main

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/pierluigizagaria/demonware-server/internal/auth"
)

func acceptOne(t *testing.T, send string) (net.Conn, []byte) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	ln := auth.NewProxyListener(inner)

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
