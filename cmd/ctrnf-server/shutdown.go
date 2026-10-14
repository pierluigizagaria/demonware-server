package main

import (
	"errors"
	"time"
)

var errDrainTimeout = errors.New("overall shutdown drain limit expired; forcing process exit with the instance lock held")

// drainPlan belongs to the entrypoint, never to request handlers. The same
// resource list handles partial startup and signal-driven shutdown.
type drainPlan struct {
	admission     []func()
	stop          []func()
	wait          []func() error
	closeDatabase func() error
}

func (p drainPlan) run() error {
	for _, stop := range p.admission {
		stop()
	}
	for _, stop := range p.stop {
		stop()
	}
	var errs []error
	for _, wait := range p.wait {
		errs = append(errs, wait())
	}
	// CtrSQLite.Close closes its pool before releasing the OS instance lock.
	if p.closeDatabase != nil {
		errs = append(errs, p.closeDatabase())
	}
	return errors.Join(errs...)
}

// One budget covers admission stop, every worker/callback, DB.Close and unlock.
// On timeout the caller must exit the process, not attempt further cleanup.
func drainWithin(limit time.Duration, p drainPlan) error {
	done := make(chan error, 1)
	timer := time.NewTimer(limit)
	defer timer.Stop()
	go func() { done <- p.run() }()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errDrainTimeout
	}
}
