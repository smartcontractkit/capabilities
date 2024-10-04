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

					// ./nx run cll:build && ./nx run kvstore:build && ./bin/cll client build && ./bin/cll node refresh --nodes=1,2 && ./bin/cll jobs add -j bootstrap -n 1 && ./bin/cll capabilities add -c kvstore -n 2,3,4,5 --bootstrap-node-id=1 && ./bin/cll contracts ocr configure --nodeIDs 2,3,4,5

					// Looking for
					// [ERROR] TrackConfig: error during LatestBlockHeight()

					// ==================================================
					// Manual process
					// Bootstrap everything + bootstrapper. Configure contract. Wait for config to show up, hardcode it in the config tracker of KV store. Start OCR
					// ==================================================
					// STEP 1: ./nx run cll:build && ./bin/cll client build && ./bin/cll node refresh --nodes=1,2 && ./bin/cll jobs add -j bootstrap -n 1 && ./bin/cll contracts ocr configure --nodeIDs 2,3,4,5
					// STEP 2: Copy paste config to configtracker impl
					// - Config tracker
					// - Config digester
					// - From account
					// STEP 3: ./nx run cll:build && ./nx run kvstore:build && ./bin/cll capabilities add -c kvstore -n 2 --bootstrap-node-id=1
					return nil
				},
			},
		},
	},
}

/*
`{
	"ConfigCount":29, // This would be hard to match between bootstrap and KV store
	"Signers":["azhPPNfXvCmNIPFoUyzdt7EW/R4=","0BLIw9QHdgADEST8EoihMae7Apg=","vCGG2iuOZ5v1ABVXrXiDoh4fheU=","QbUgrs/L4eT7/H9nkGI0NbKDYrg="],
	"Transmitters":["0x1D8E22c497F1BD188Cf57c14095c378B1f4763BD","0x81f8229eaBAa88023CEB818c3f86Cdfc1Af1F8A8","0xD38dB00fe9ae977D28192FF302f69c2975cE126E","0x427AA264121BdaB256e314cbC1aBc06561eD1c49"],
	"F":1,
	"OnchainConfig":"",
	"OffchainConfigVersion":30,
	"OffchainConfig":"yAGA5JfQEtABgOSX0BLYAYCo1rkH4AGAyrXuAegBgNiO4W/wAQr6AQQCAwQFggIgYfBrtZOaI/0Aawz/GJ4ht8BBRB7chftRbtf+o1l8wAmCAiBZ9Hp1flxUB3a0dRBuzCH7LE306gYCcn60XOMghW9sDYICILLxsanWNGwreM0z0IVIBEtmIhdWy8nLSxgSV+/XYi+YggIgdTOWjP4OqMV9GYUnn1btk421+Y9LcJ9qjzzJUSCYmHOKAjQxMkQzS29vV0R6ZUgydWJZQWlUMnN5a1U0MjI4WWo2TEc0ZzJnRFBZR3ljYmo0b0txWDdZigI0MTJEM0tvb1dGMlFFSmJNZ0R3VDRDbXhGdW9BYlgxUEFBYmVmNFRwVTVtYlN5enFBZ3lDQYoCNDEyRDNLb29XUUhoMUxZOXFvb2I5N1I4NWtuRzNXMjR5TlB2RFdDR2pTeG9Mb3FXZE5VSnmKAjQxMkQzS29vV0pweTFuVllYbVd6anVCclBzcVRGcm1zU0VVZFFnZWp1NGJGdjV4Nmd3Tmh3mAKAlOvcA6ACgJTr3AOoAoCU69wDsAKAlOvcA7oCjAEKIIonz72hRGemuxPxy1ugKhxbdzlpn0c3KyRvk9nMyD9SEiBQHlHh5AgoD60EZYBZdNPHI7pMZfV+mz/2JyFBcCv5sBoQivYwoRU2BzLbhEvSWihLPxoQG7Hg2yhKTD8dj+lHKzUstRoQMX/mg/glcoxS+DSIBtaY3hoQfzVrWGo0/tRF6I8WVkcYYcACgOSX0BLIAoCU69wD"
	}`

	- Why is signer and transmitter different?
*/
