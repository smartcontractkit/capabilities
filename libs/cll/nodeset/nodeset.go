package nodeset

import "github.com/urfave/cli/v2"

var Commands = []*cli.Command{
	{
		Name:  "nodeset",
		Usage: "Commands to manage local nodesets",
		Subcommands: []*cli.Command{
			{
				Name:  "start",
				Usage: "Start the anvil client",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "fresh",
						Usage: "Re-creates the nodeset from scratch",
						Value: false,
					},
				},
				Action: func(c *cli.Context) error {
					// Refresh nodeset
					// (Re)Deploy OCR contract
					// Add bootstrap spec
					// ./nx run cll:build && ./nx run kvstore:build && ./bin/cll jobs add -j bootstrap -n 1 && ./bin/cll capabilities add -c kvstore -n 2 --bootstrap-node-id=1
					// Add KV specs - KV specs include bootstrapper
					// Set config
					// ./nx run cll:build && ./bin/cll contracts ocr configure --nodeIDs 2,3,4,5
					return nil
				},
			},
		},
	},
}
