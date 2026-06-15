package tui

import (
	"sync"
	"time"

	"github.com/xnet-admin-1/ax/internal/engine"
)

type eventBatcher struct {
	mu       sync.Mutex
	events   []engine.Event
	interval time.Duration
	timer    *time.Timer
	flushFn  func([]engine.Event)
}

func newEventBatcher(interval time.Duration, flushFn func([]engine.Event)) *eventBatcher {
	return &eventBatcher{interval: interval, flushFn: flushFn}
}

func (b *eventBatcher) add(ev engine.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, ev)
	if b.timer == nil {
		b.timer = time.AfterFunc(b.interval, b.flush)
	}
}

func (b *eventBatcher) flush() {
	b.mu.Lock()
	events := b.events
	b.events = nil
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
	if len(events) > 0 {
		b.flushFn(events)
	}
}

func (b *eventBatcher) stop() {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
}
