package platform

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdmissionTransfersOnceWithoutSessionExpiry(t *testing.T) {
	var ended atomic.Int32
	a := NewAdmission(func() { ended.Add(1) })
	a.HoldUntil(time.Now().Add(time.Hour))
	if !a.Claim() || a.Claim() {
		t.Fatal("admission must be claimed exactly once")
	}
	a.Expire()
	if ended.Load() != 0 {
		t.Fatal("handoff expiry ended an established connection")
	}
	a.Close()
	a.Close()
	if ended.Load() != 1 || a.Claim() {
		t.Fatal("close must release exactly once")
	}
}

func TestAdmissionPendingExpiryAndClaimRace(t *testing.T) {
	for range 100 {
		var ended atomic.Int32
		a := NewAdmission(func() { ended.Add(1) })
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); a.Claim() }()
		go func() { defer wait.Done(); a.Expire() }()
		wait.Wait()
		a.Close()
		if ended.Load() != 1 {
			t.Fatal("race leaked or double-ended admission")
		}
	}
	a := NewAdmission(nil)
	a.Expire()
	if a.Claim() {
		t.Fatal("expired admission was claimed")
	}
	var empty *Admission
	if !empty.Claim() {
		t.Fatal("providers without native sessions must remain supported")
	}
}
