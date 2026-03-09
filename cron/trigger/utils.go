package trigger

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func enforceFastestSchedule(lggr logger.Logger, clock clockwork.Clock, jobDef gocron.JobDefinition, maximumFastest time.Duration) caperrors.Error {
	var options []gocron.SchedulerOption
	// Set scheduler location to UTC for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	// Use passed in clock
	options = append(options, gocron.WithClock(clock))

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

	nextRuns, err := tempJob.NextRuns(2)
	if err != nil {
		return caperrors.NewPublicSystemError(fmt.Errorf("failed to initialize next runs: %w", err), caperrors.Internal)
	}

	if len(nextRuns) != 2 {
		return caperrors.NewPublicSystemError(errors.New("could not determine next two scheduled runs"), caperrors.Internal)
	}

	if nextRuns[1].Before(nextRuns[0].Add(maximumFastest)) {
		return caperrors.NewPublicUserError(fmt.Errorf("maximum fastest cron schedule is %s", maximumFastest.String()), caperrors.InvalidArgument)
	}

	return nil
}
