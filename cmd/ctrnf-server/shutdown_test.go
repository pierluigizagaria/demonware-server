package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NextendoNetwork/nextendo-demonware/internal/config"
	"github.com/NextendoNetwork/nextendo-demonware/internal/platform/guest"
	"github.com/NextendoNetwork/nextendo-demonware/internal/store"
)

func TestDrainOrdersAdmissionWorkersAndDatabase(t *testing.T) {
	var order []string
	waitErr := errors.New("completed worker reported an error")
	p := drainPlan{
		admission:     []func(){func() { order = append(order, "admission") }},
		stop:          []func(){func() { order = append(order, "stop") }},
		wait:          []func() error{func() error { order = append(order, "callback"); return waitErr }, func() error { order = append(order, "workers"); return nil }},
		closePlatform: func() error { order = append(order, "platform"); return nil },
		closeDatabase: func() error { order = append(order, "database"); return nil },
	}
	if err := drainWithin(time.Second, p); !errors.Is(err, waitErr) {
		t.Fatalf("lost worker error: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"admission", "stop", "callback", "workers", "platform", "database"}) {
		t.Fatalf("order: %v", order)
	}
}

func TestDrainTimeoutCannotCloseDatabaseUnderWorker(t *testing.T) {
	release, finished := make(chan struct{}), make(chan struct{})
	var closed atomic.Bool
	p := drainPlan{wait: []func() error{func() error { <-release; return nil }}, closeDatabase: func() error { closed.Store(true); close(finished); return nil }}
	err := drainWithin(20*time.Millisecond, p)
	if !errors.Is(err, errDrainTimeout) || closed.Load() {
		t.Fatal("database closed while worker was active")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("test worker did not finish")
	}
}

func TestOverallDrainLimitIncludesDatabaseClose(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unlocked atomic.Bool
	p := drainPlan{closeDatabase: func() error { close(entered); <-release; unlocked.Store(true); close(finished); return nil }}
	err := drainWithin(20*time.Millisecond, p)
	<-entered
	if !errors.Is(err, errDrainTimeout) || unlocked.Load() {
		t.Fatal("database close escaped overall budget or released lock early")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("test database close did not finish")
	}
}

func TestStartupFailureReleasesOpenedDatabase(t *testing.T) {
	// An invalid local port fails before any network listener is opened. This
	// exercises real partial-startup cleanup with a temporary SQLite database.
	cfg := config.ServerConfig{Admission: "guest", UnlockAll: true, DataDir: t.TempDir(), DashPort: -1, RedactLogs: true}
	if err := runServer(context.Background(), cfg); err == nil {
		t.Fatal("expected dashboard startup failure")
	}
	db, err := store.OpenCTRSQLite(filepath.Join(cfg.DataDir, store.CtrDB), true, guest.GuestTrustID)
	if err != nil {
		t.Fatalf("startup retained database lock: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.Db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatal("schema changed during failed startup")
	}
}
