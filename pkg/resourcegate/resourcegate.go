// Package resourcegate provides a capacity-N semaphore that also remembers,
// purely for the caller's own logging, the name last passed to a successful
// Acquire. It does not know what the permit protects or why — the "is this
// actually a conflict" judgment stays with the caller.
package resourcegate

import (
	"context"
	"sync"
)

// Gate is a capacity-N semaphore with a named holder for diagnostics.
type Gate struct {
	sem chan struct{} // capacity N: a send = held one more permit, a receive = released one
	mu  sync.Mutex
	who string // name passed to the most recent successful Acquire not yet released
}

// New returns a Gate with the given permit capacity.
func New(capacity int) *Gate {
	return &Gate{sem: make(chan struct{}, capacity)}
}

// Holder returns the name passed to the most recent successful Acquire that
// has not yet been released, or "" if no permit is currently held.
func (g *Gate) Holder() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.who
}

func (g *Gate) setHolder(name string) {
	g.mu.Lock()
	g.who = name
	g.mu.Unlock()
}

// Acquire blocks until a permit is available or ctx is done. On success,
// holder becomes visible via Holder() until the returned release func is
// called; release is idempotent, so a defer alongside an explicit early
// release never double-frees the permit.
func (g *Gate) Acquire(ctx context.Context, holder string) (release func(), err error) {
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}

	g.setHolder(holder)

	var once sync.Once
	return func() {
		once.Do(func() {
			g.setHolder("")
			<-g.sem
		})
	}, nil
}
