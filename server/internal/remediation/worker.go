package remediation

import (
	"context"
	"log"
)

// jobQueueSize bounds the buffered channel of pending investigation jobs. When
// full, Enqueue drops the job (the issue is still recorded; an admin can act on
// it) rather than blocking the request path.
const jobQueueSize = 128

// job is one unit of work for the worker pool: investigate a fresh issue, or
// resume a parked one after an admin decision. Carrying the kind lets a single
// queue + pool serve both the initial investigation and the post-approval resume.
type job struct {
	issueID int64
	resume  bool
}

// Enqueue schedules a read-only investigation of issueID on the worker pool. It
// is non-blocking and safe to call from a request handler: if the feature is off
// or the queue is full it simply no-ops (the issue row already exists). The
// queue is created lazily in NewService, so this is always safe to call.
func (s *Service) Enqueue(issueID int64) {
	s.enqueue(job{issueID: issueID})
}

// EnqueueResume schedules a resume of a parked issue after an admin approved or
// denied a proposal. Non-blocking and drop-on-full like Enqueue (the proposal
// outcome is already persisted; an admin can re-trigger if the queue overflowed).
func (s *Service) EnqueueResume(issueID int64) {
	s.enqueue(job{issueID: issueID, resume: true})
}

func (s *Service) enqueue(j job) {
	if s.jobs == nil {
		return
	}
	select {
	case s.jobs <- j:
	default:
		log.Printf("remediation: job queue full; dropping job for issue %d (resume=%v)", j.issueID, j.resume)
	}
}

// StartWorkers launches n goroutines that drain the job queue and run the Runner
// for each issue. It returns immediately; the workers stop when ctx is cancelled.
// Wire this in main.go after the Runner is constructed (which needs the
// ToolServer). n<=0 defaults to 2.
func (s *Service) StartWorkers(ctx context.Context, runner *Runner, n int) {
	if s.jobs == nil || runner == nil {
		return
	}
	if n <= 0 {
		n = 2
	}
	for i := 0; i < n; i++ {
		go func(worker int) {
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-s.jobs:
					var err error
					if j.resume {
						err = runner.Resume(ctx, j.issueID)
					} else {
						err = runner.Run(ctx, j.issueID)
					}
					if err != nil {
						log.Printf("remediation: worker %d job issue %d (resume=%v): %v", worker, j.issueID, j.resume, err)
					}
				}
			}
		}(i)
	}
}
