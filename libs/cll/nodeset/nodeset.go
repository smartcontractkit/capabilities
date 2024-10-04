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
					// Add KV specs - KV specs include bootstrapper
					// Set config
					// ==================================================
					// TWO NODES ONLY
					// ==================================================
					// ./nx run cll:build && ./nx run kvstore:build && ./bin/cll client build && ./bin/cll node refresh --nodes=1,2 && ./bin/cll jobs add -j bootstrap -n 1 && ./bin/cll capabilities add -c kvstore -n 2 --bootstrap-node-id=1 && ./bin/cll contracts ocr configure --nodeIDs 2,3,4,5

					// ==================================================
					// ALL NODES
					// ==================================================

					// ./nx run cll:build && ./nx run kvstore:build &&  ./bin/cll node refresh --nodes=1,2,3,4,5 && ./bin/cll jobs add -j bootstrap -n 1 && ./bin/cll capabilities add -c kvstore -n 2,3,4,5 --bootstrap-node-id=1 && ./bin/cll contracts ocr configure --nodeIDs 2,3,4,5

					// Looking for
					// [ERROR] TrackConfig: error during LatestBlockHeight()
					return nil
				},
			},
		},
	},
}
