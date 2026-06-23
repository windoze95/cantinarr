package remediation

import (
	"context"
	"log"
)

// jobQueueSize bounds the buffered channel of pending investigation jobs. When
// full, Enqueue drops the job (the issue is still recorded; an admin can act on
// it) rather than blocking the request path.
const jobQueueSize = 128

// Enqueue schedules a read-only investigation of issueID on the worker pool. It
// is non-blocking and safe to call from a request handler: if the feature is off
// or the queue is full it simply no-ops (the issue row already exists). The
// queue is created lazily in NewService, so this is always safe to call.
func (s *Service) Enqueue(issueID int64) {
	if s.jobs == nil {
		return
	}
	select {
	case s.jobs <- issueID:
	default:
		log.Printf("remediation: job queue full; not auto-investigating issue %d", issueID)
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
				case issueID := <-s.jobs:
					if err := runner.Run(ctx, issueID); err != nil {
						log.Printf("remediation: worker %d run issue %d: %v", worker, issueID, err)
					}
				}
			}
		}(i)
	}
}
