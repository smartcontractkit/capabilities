package trigger

import (
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func enforceFastestSchedule(lggr logger.Logger, jobDef gocron.JobDefinition, maximumFastest time.Duration) caperrors.Error {
	var options []gocron.SchedulerOption
	// Use a fixed location and point in time for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	options = append(options, gocron.WithClock(clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))))

	tempScheduler, err := gocron.NewScheduler(options...)
	if err != nil {
		return caperrors.NewPublicSystemError(fmt.Errorf("failed to initialize temp scheduler: %w", err), caperrors.Internal)
	}
	tempJob, err := tempScheduler.NewJob(jobDef, gocron.NewTask(func() {}))
	if err != nil {
		return caperrors.NewPublicUserError(fmt.Errorf("failed to initialize job: %w", err), caperrors.InvalidArgument)
	}
	tempScheduler.Start()
	defer func() {
		err := tempScheduler.Shutdown()
		if err != nil {
			lggr.Errorw("error shutting down enforceFastestSchedule temporary scheduler")
		}
	}()

	// We need to check several runs to make sure there are enough to catch any short gaps (see unit test).
	// 12 is technically not enough in a general case but should work in practice when maximumFastest is between 5 and 60 seconds.
	nextRuns, err := tempJob.NextRuns(12)
	if err != nil || len(nextRuns) < 12 {
		return caperrors.NewPublicSystemError(fmt.Errorf("failed to initialize next runs: %w", err), caperrors.Internal)
	}

	for i := 1; i < len(nextRuns); i++ {
		if nextRuns[i].Before(nextRuns[i-1].Add(maximumFastest)) {
			return caperrors.NewPublicUserError(fmt.Errorf("maximum fastest cron schedule is %s", maximumFastest.String()), caperrors.LimitExceeded)
		}
	}

	return nil
}
