package downloader

import (
	"sync"
	"time"
)

type progress struct {
	mu      sync.Mutex
	state   Progress
	parts   map[int64]int64
	done    chan struct{}
	stopped chan struct{}
}

func newProgress(callback func(Progress)) *progress {
	p := &progress{state: Progress{Total: -1}, parts: make(map[int64]int64)}
	if callback == nil {
		return p
	}
	p.done, p.stopped = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(p.stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		callback(p.snapshot())
		for {
			select {
			case <-ticker.C:
				callback(p.snapshot())
			case <-p.done:
				callback(p.snapshot())
				return
			}
		}
	}()
	return p
}
func (p *progress) reset(total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = Progress{Total: total}
	clear(p.parts)
}
func (p *progress) active(delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.ActiveWorkers += delta
}
func (p *progress) set(key, n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n > p.parts[key] {
		p.state.Downloaded += n - p.parts[key]
		p.parts[key] = n
	}
}

func (p *progress) complete(key int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.parts, key)
}
func (p *progress) snapshot() Progress { p.mu.Lock(); defer p.mu.Unlock(); return p.state }
func (p *progress) close() {
	if p.done != nil {
		close(p.done)
		<-p.stopped
	}
}
