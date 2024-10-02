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
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to create",
					},
				},
				Action: func(c *cli.Context) error {
					return createNodes(c.Int("nodes"))
				},
			},
			{
				Name:  "remove",
				Usage: "Remove local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to remove",
					},
				},
				Action: func(c *cli.Context) error {
					return removeNodes(c.Int("nodes"))
				},
			},
			{
				Name:  "reset",
				Usage: "Reset local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to reset",
					},
				},
				Action: func(c *cli.Context) error {
					err := removeNodes(c.Int("nodes"))
					if err != nil {
						return err
					}

					return createNodes(c.Int("nodes"))
				},
			},
			{
				Name:  "start",
				Usage: "Start local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to start",
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
						nodes: c.Int("nodes"),
						logs:  c.Bool("logs"),
					})
				},
			},
			{
				Name:  "stop",
				Usage: "Stop local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to stop",
					},
				},
				Action: func(c *cli.Context) error {
					return stopNodes(c.Int("nodes"))
				},
			},
			{
				Name:  "refresh",
				Usage: "Refresh local nodes",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "nodes",
						Value: 1,
						Usage: "Number of nodes to start",
					},
					&cli.BoolFlag{
						Aliases: []string{"l"},
						Name:    "logs",
						Value:   false,
						Usage:   "Redirect logs to terminal",
					},
				},
				Action: func(c *cli.Context) error {
					err := stopNodes(c.Int("nodes"))
					if err != nil {
						return err
					}
					err = removeNodes(c.Int("nodes"))
					if err != nil {
						return err
					}

					err = createNodes(c.Int("nodes"))
					if err != nil {
						return err
					}

					return startNodes(startNodesArgs{
						nodes: c.Int("nodes"),
						logs:  c.Bool("logs"),
					})
				},
			},
		},
	},
}
