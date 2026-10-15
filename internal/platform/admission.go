package platform

import (
	"sync"
	"time"
)

// Admission transfers a provider session from authentication to one lobby
// connection. Only an unclaimed admission expires; claimed connections do not.
type Admission struct {
	mu              sync.Mutex
	claimed, closed bool
	timer           *time.Timer
	end             func()
}

func NewAdmission(end func()) *Admission { return &Admission{end: end} }

func (a *Admission) HoldUntil(deadline time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.claimed || a.closed {
		return
	}
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(time.Until(deadline), a.Expire)
}

func (a *Admission) Claim() bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.claimed || a.closed {
		return false
	}
	a.claimed = true
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	return true
}

func (a *Admission) Expire() { a.close(true) }
func (a *Admission) Close()  { a.close(false) }

func (a *Admission) close(onlyPending bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.closed || (onlyPending && a.claimed) {
		a.mu.Unlock()
		return
	}
	a.closed = true
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	end := a.end
	a.end = nil
	a.mu.Unlock()
	if end != nil {
		end()
	}
}
