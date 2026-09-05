package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"tofuhut/internal/reconciler"
)

// WorkloadSpec describes a workload to reconcile on a schedule.
type WorkloadSpec struct {
	Name     string
	Interval time.Duration
	Enabled  bool
}

// Options configure scheduler behavior.
type Options struct {
	Jitter        time.Duration
	MaxConcurrent int
}

type scheduledWorkload struct {
	mu       sync.RWMutex
	name     string
	interval time.Duration
	cancel   context.CancelFunc
}

func (w *scheduledWorkload) getInterval() time.Duration {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.interval
}

func (w *scheduledWorkload) setInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// Scheduler runs reconciliation for workloads at configured intervals.
type Scheduler struct {
	runner reconciler.Runner
	jitter time.Duration
	sem    chan struct{}

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	workloads map[string]*scheduledWorkload

	wg     sync.WaitGroup
	randMu sync.Mutex
	rand   *lockedRand
}

// New builds a scheduler for the given workloads.
func New(runner reconciler.Runner, specs []WorkloadSpec, opts Options) *Scheduler {
	var sem chan struct{}
	if opts.MaxConcurrent > 0 {
		sem = make(chan struct{}, opts.MaxConcurrent)
	}
	s := &Scheduler{
		runner:    runner,
		jitter:    opts.Jitter,
		sem:       sem,
		rand:      newLockedRand(),
		workloads: make(map[string]*scheduledWorkload),
	}
	for _, spec := range specs {
		if !spec.Enabled || spec.Interval <= 0 {
			continue
		}
		s.workloads[spec.Name] = &scheduledWorkload{
			name:     spec.Name,
			interval: spec.Interval,
		}
	}
	return s
}

// Start launches reconciliation loops for enabled workloads.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.cancel = context.WithCancel(ctx)
	for _, w := range s.workloads {
		wCtx, wCancel := context.WithCancel(s.ctx)
		w.cancel = wCancel
		s.wg.Add(1)
		go s.runWorkload(wCtx, w)
	}
}

// UpdateSpecs synchronizes running workload loops with new specs without cancelling active runs.
func (s *Scheduler) UpdateSpecs(specs []WorkloadSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()

	validSpecs := make(map[string]WorkloadSpec)
	for _, spec := range specs {
		if spec.Enabled && spec.Interval > 0 {
			validSpecs[spec.Name] = spec
		}
	}

	// 1. Stop workloads that are no longer in specs or disabled
	for name, w := range s.workloads {
		if _, ok := validSpecs[name]; !ok {
			if w.cancel != nil {
				w.cancel()
			}
			delete(s.workloads, name)
		}
	}

	// 2. Add new workloads or update existing ones
	for name, spec := range validSpecs {
		if existing, ok := s.workloads[name]; ok {
			existing.setInterval(spec.Interval)
		} else {
			w := &scheduledWorkload{
				name:     name,
				interval: spec.Interval,
			}
			s.workloads[name] = w
			if s.ctx != nil && s.ctx.Err() == nil {
				wCtx, wCancel := context.WithCancel(s.ctx)
				w.cancel = wCancel
				s.wg.Add(1)
				go s.runWorkload(wCtx, w)
			}
		}
	}
}

// Stop cancels all running workloads and waits for them to exit.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Wait blocks until all workload loops exit.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) runWorkload(ctx context.Context, w *scheduledWorkload) {
	defer s.wg.Done()
	logrus.WithFields(logrus.Fields{
		"component": "scheduler",
		"workload":  w.name,
		"interval":  w.getInterval().String(),
	}).Info("scheduler started")

	first := true
	for {
		if !first {
			if !sleepWithContext(ctx, w.getInterval()+s.jitterDuration()) {
				return
			}
		}
		first = false

		if !s.acquire(ctx) {
			return
		}

		start := time.Now()
		runID := newScheduledRunID()
		runCtx := reconciler.WithTriggerSource(reconciler.WithRequestID(ctx, runID), "scheduler")
		err := s.runner.Run(runCtx, w.name)
		latency := time.Since(start)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"component":  "scheduler",
				"workload":   w.name,
				"request_id": runID,
				"latency":    latency.String(),
			}).Warn("scheduled workload run failed")
		} else {
			logrus.WithFields(logrus.Fields{
				"component":  "scheduler",
				"workload":   w.name,
				"request_id": runID,
				"latency":    latency.String(),
			}).Info("scheduled workload run completed")
		}

		s.release()
	}
}

func (s *Scheduler) acquire(ctx context.Context) bool {
	if s.sem == nil {
		return true
	}
	select {
	case s.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scheduler) release() {
	if s.sem == nil {
		return
	}
	<-s.sem
}

func (s *Scheduler) jitterDuration() time.Duration {
	if s.jitter <= 0 {
		return 0
	}
	s.randMu.Lock()
	defer s.randMu.Unlock()
	return s.rand.Duration(s.jitter)
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func newScheduledRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("sched-%d", time.Now().UnixNano())
	}
	return "sched-" + hex.EncodeToString(buf)
}
