package faststart

import (
	"container/list"
	"errors"
	"io"
	"sync"
)

// PlanKey identifies one version of a source file: a rewritten clip (new size
// or mtime) gets a fresh plan.
type PlanKey struct {
	ID, Size, Mtime int64
}

// PlanCache keeps recently built plans in memory, bounded by the total moov
// bytes they hold (a 5-track OBS clip carries about 250 KB). Refusals are
// cached too, so a file that cannot be relocated is parsed once per version.
type PlanCache struct {
	MaxBytes int64

	mu    sync.Mutex
	bytes int64
	ll    *list.List
	items map[PlanKey]*list.Element
	// inflight dedupes concurrent builds for the same key: a video element
	// fires several Range requests at once on open.
	inflight map[PlanKey]*planCall
}

type planEntry struct {
	key  PlanKey
	plan *Plan // nil: not applicable
}

type planCall struct {
	done chan struct{}
	plan *Plan
	err  error
}

func (e *planEntry) cost() int64 {
	if e.plan == nil {
		return 64
	}
	return int64(len(e.plan.moov)) + 64
}

// Get returns the plan for key, building it from r on a miss. A nil plan with
// a nil error means the file is served as is. Read errors are not cached.
func (c *PlanCache) Get(key PlanKey, r io.ReaderAt) (*Plan, error) {
	c.mu.Lock()
	if c.items == nil {
		c.items = map[PlanKey]*list.Element{}
		c.inflight = map[PlanKey]*planCall{}
		c.ll = list.New()
	}
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		p := el.Value.(*planEntry).plan
		c.mu.Unlock()
		return p, nil
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-call.done
		return call.plan, call.err
	}
	call := &planCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	p, err := PlanFor(r, key.Size)
	if errors.Is(err, ErrNotApplicable) {
		p, err = nil, nil
	}
	call.plan, call.err = p, err

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		e := &planEntry{key: key, plan: p}
		c.items[key] = c.ll.PushFront(e)
		c.bytes += e.cost()
		for c.MaxBytes > 0 && c.bytes > c.MaxBytes && c.ll.Len() > 1 {
			old := c.ll.Back()
			oe := old.Value.(*planEntry)
			c.ll.Remove(old)
			delete(c.items, oe.key)
			c.bytes -= oe.cost()
		}
	}
	c.mu.Unlock()
	close(call.done)
	return p, err
}
