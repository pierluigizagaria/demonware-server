package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type closingConnection struct{ entered, release chan struct{} }

func (c *closingConnection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c *closingConnection) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }
func (c *closingConnection) Close() error                        { close(c.entered); <-c.release; return nil }

type closingConnector struct{ conn *closingConnection }

func (c closingConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c closingConnector) Driver() driver.Driver                        { return closingDriver{c} }

type closingDriver struct{ connector closingConnector }

func (d closingDriver) Open(string) (driver.Conn, error) { return d.connector.conn, nil }

func TestInstanceLockOutlivesDatabaseClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.db")
	lock, err := lockInstance(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	conn := &closingConnection{entered: make(chan struct{}), release: make(chan struct{})}
	db := sql.OpenDB(closingConnector{conn})
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	s := &SQLite{db: db, lock: lock}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	<-conn.entered
	second, err := lockInstance(path)
	if err == nil {
		second.Close()
		t.Error("lock released while DB.Close was blocked")
	}
	close(conn.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("database close did not finish")
	}
	second, err = lockInstance(path)
	if err != nil {
		t.Fatalf("lock retained after DB.Close: %v", err)
	}
	second.Close()
}
