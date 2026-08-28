// Command cron runs the cron trigger capability as its own binary.
//
// It hosts no node of its own: the capabilities registry it announces itself to,
// and the settings its limits resolve against, come from the crecore process it
// is pointed at (--capabilities.proxy-url). What is left here is the capability
// itself; everything else - flags, observability, serving, announcing - is Run.
package main

import (
	"context"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/capability"
)

func main() {
	capability.Run(context.Background(), trigger.NewCron, capability.WithOtelViews(trigger.MetricViews()...))
}
