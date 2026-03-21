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

// Scheduler runs reconciliation for workloads at configured intervals.
type Scheduler struct {
	runner reconciler.Runner
	specs  []WorkloadSpec
	jitter time.Duration
	sem    chan struct{}

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
	return &Scheduler{
		runner: runner,
		specs:  specs,
		jitter: opts.Jitter,
		sem:    sem,
		rand:   newLockedRand(),
	}
}

// Start launches reconciliation loops for enabled workloads.
func (s *Scheduler) Start(ctx context.Context) {
	for _, spec := range s.specs {
		if !spec.Enabled || spec.Interval <= 0 {
			continue
		}
		spec := spec
		s.wg.Add(1)
		go s.runWorkload(ctx, spec)
	}
}

// Wait blocks until all workload loops exit.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) runWorkload(ctx context.Context, spec WorkloadSpec) {
	defer s.wg.Done()
	logrus.WithFields(logrus.Fields{
		"component": "scheduler",
		"workload":  spec.Name,
		"interval":  spec.Interval.String(),
	}).Info("scheduler started")

	first := true
	for {
		if !first {
			if !sleepWithContext(ctx, spec.Interval+s.jitterDuration()) {
				return
			}
		}
		first = false

		if !s.acquire(ctx) {
			return
		}

		start := time.Now()
		runID := newScheduledRunID()
		runCtx := reconciler.WithRequestID(ctx, runID)
		err := s.runner.Run(runCtx, spec.Name)
		latency := time.Since(start)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"component":  "scheduler",
				"workload":   spec.Name,
				"request_id": runID,
				"latency":    latency.String(),
			}).Warn("scheduled workload run failed")
		} else {
			logrus.WithFields(logrus.Fields{
				"component":  "scheduler",
				"workload":   spec.Name,
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
