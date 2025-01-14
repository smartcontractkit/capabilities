package trigger

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"
)

func enforceFastestSchedule(clock clockwork.Clock, jobDef gocron.JobDefinition, maximumFastest time.Duration) error {
	var options []gocron.SchedulerOption
	// Set scheduler location to UTC for consistency across nodes.
	options = append(options, gocron.WithLocation(time.UTC))
	// Use passed in clock
	options = append(options, gocron.WithClock(clock))

	tempScheduler, err := gocron.NewScheduler(options...)
	if err != nil {
		return err
	}
	tempJob, err := tempScheduler.NewJob(jobDef, gocron.NewTask(func() {}))
	if err != nil {
		return err
	}
	tempScheduler.Start()
	nextRuns, err := tempJob.NextRuns(2)
	if err != nil {
		return err
	}
	err = tempScheduler.Shutdown()
	if err != nil {
		return err
	}

	if len(nextRuns) != 2 {
		return errors.New("could not determine next two scheduled runs")
	}

	if nextRuns[1].Before(nextRuns[0].Add(maximumFastest)) {
		return fmt.Errorf("maximum fastest cron schedule is %s", maximumFastest.String())
	}

	return nil
}
