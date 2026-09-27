// Package bus is the ordered, replayable event log. One sequencer goroutine
// writes; any number of reader goroutines fan out without taking locks.
package bus

import (
	"sync/atomic"
)

// Entry is one encoded envelope. Data is serialized once by the sequencer and
// shared by every reader.
type Entry struct {
	Seq   uint64
	Topic uint8 // proto.Mask* bit
	Data  []byte
}

// Ring holds the most recent 2^n entries. Sequence numbers start at 1.
type Ring struct {
	slots []atomic.Pointer[Entry]
	mask  uint64
	head  atomic.Uint64 // seq of the last published entry (0 = empty)
	gen   atomic.Pointer[chan struct{}]
}

func NewRing(sizePow2 int) *Ring {
	if sizePow2&(sizePow2-1) != 0 || sizePow2 == 0 {
		panic("ring size must be a power of two")
	}
	r := &Ring{slots: make([]atomic.Pointer[Entry], sizePow2), mask: uint64(sizePow2 - 1)}
	ch := make(chan struct{})
	r.gen.Store(&ch)
	return r
}

func (r *Ring) Size() uint64 { return r.mask + 1 }
func (r *Ring) Head() uint64 { return r.head.Load() }

// Oldest is the smallest seq still held.
func (r *Ring) Oldest() uint64 {
	h := r.head.Load()
	if h < r.Size() {
		return 1
	}
	return h - r.Size() + 1
}

// Put stores an entry at its slot without publishing it. Sequencer only.
func (r *Ring) Put(e *Entry) { r.slots[e.Seq&r.mask].Store(e) }

// Publish makes everything up to seq visible and wakes all waiting readers
// with a single channel close. Sequencer only.
func (r *Ring) Publish(seq uint64) {
	r.head.Store(seq)
	next := make(chan struct{})
	old := r.gen.Swap(&next)
	close(*old)
}

// Wait returns a channel that is closed on the next Publish. Load it before
// reading Head so a publish in between is never missed.
func (r *Ring) Wait() <-chan struct{} { return *r.gen.Load() }

// Get returns the entry for seq, or nil if it has been overwritten (reader lapped).
func (r *Ring) Get(seq uint64) *Entry {
	e := r.slots[seq&r.mask].Load()
	if e == nil || e.Seq != seq {
		return nil
	}
	return e
}
