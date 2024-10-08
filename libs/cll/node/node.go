package node

import (
	"github.com/urfave/cli/v2"
)

var Commands = []*cli.Command{
	{
		Name:  "node",
		Usage: "Commands to manage the local nodes",
		Subcommands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create new local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodeIDs",
						Usage:    "Node IDs to create",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return createNodes(c.IntSlice("nodes"))
				},
			},
			{
				Name:  "remove",
				Usage: "Remove local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to remove",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return removeNodes(c.IntSlice("nodes"))
				},
			},
			{
				Name:  "reset",
				Usage: "Reset local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to resets",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					err := removeNodes(c.IntSlice("nodes"))
					if err != nil {
						return err
					}

					return createNodes(c.IntSlice("nodes"))
				},
			},
			{
				Name:  "start",
				Usage: "Start local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to start",
						Required: true,
					},
					&cli.BoolFlag{
						Aliases: []string{"l"},
						Name:    "logs",
						Value:   false,
						Usage:   "Redirect logs to terminal",
					},
				},
				Action: func(c *cli.Context) error {
					return startNodes(startNodesArgs{
						nodeIDs: c.IntSlice("nodes"),
						logs:    c.Bool("logs"),
					})
				},
			},
			{
				Name:  "stop",
				Usage: "Stop local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to stop",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return stopNodes(c.IntSlice("nodes"))
				},
			},
			{
				Name:  "fetch-keys",
				Usage: "Fetch keys from the local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to fetch keys from",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return FetchKeys(c.IntSlice("nodes"))
				},
			},
			{
				Name:  "refresh",
				Usage: "Refresh local nodes",
				Flags: []cli.Flag{
					&cli.IntSliceFlag{
						Name:     "nodes",
						Usage:    "Node IDs to refresh",
						Required: true,
					},
					&cli.BoolFlag{
						Aliases: []string{"l"},
						Name:    "logs",
						Value:   false,
						Usage:   "Redirect logs to terminal",
					},
				},
				Action: func(c *cli.Context) error {
					nodeIDs := c.IntSlice("nodes")
					err := stopNodes(nodeIDs)
					if err != nil {
						return err
					}
					err = removeNodes(nodeIDs)
					if err != nil {
						return err
					}

					err = createNodes(nodeIDs)
					if err != nil {
						return err
					}

					err = startNodes(startNodesArgs{
						nodeIDs: nodeIDs,
						logs:    c.Bool("logs"),
					})
					if err != nil {
						return err
					}
					return FetchKeys(nodeIDs)
				},
			},
		},
	},
}
