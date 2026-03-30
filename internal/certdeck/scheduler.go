package certdeck

import (
	"context"
	"sync"
	"time"
)

// Scheduler runs periodic remote cert checks (optional SSH read-only).
type Scheduler struct {
	mu       sync.Mutex
	stop     chan struct{}
	stopped  chan struct{}
	interval time.Duration
	run      func(ctx context.Context)
}

func NewScheduler(interval time.Duration, run func(ctx context.Context)) *Scheduler {
	if interval < time.Minute {
		interval = time.Hour
	}
	return &Scheduler{
		interval: interval,
		run:      run,
	}
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	s.stop = make(chan struct{})
	s.stopped = make(chan struct{})
	stop := s.stop
	stopped := s.stopped
	interval := s.interval
	run := s.run
	s.mu.Unlock()

	go func() {
		defer close(stopped)
		t := time.NewTicker(interval)
		defer t.Stop()
		ctx := context.Background()
		run(ctx)
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				run(ctx)
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	ch := s.stop
	s.stop = nil
	stopped := s.stopped
	s.mu.Unlock()
	if ch != nil {
		close(ch)
		<-stopped
	}
}
