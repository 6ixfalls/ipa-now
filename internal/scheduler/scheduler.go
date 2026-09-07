package scheduler

import (
	"context"
	"github.com/6ixfalls/ipa-now/internal/jobs"
	"time"
)

type Queue interface{ Claim() (*jobs.Job, error) }
type Runner interface {
	Run(context.Context, jobs.Job) error
	Expire() error
}

// One serial runner owns the one configured device. Queue ownership persists in SQLite.
func Run(ctx context.Context, q Queue, w Runner) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.Expire(); err != nil {
			return err
		}
		j, err := q.Claim()
		if err != nil {
			return err
		}
		if j != nil {
			if err = w.Run(ctx, *j); err != nil {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
