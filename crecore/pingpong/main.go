// Command pingpong is a sample CRE binary: it passes messages between its instances over a libocr
// binary network endpoint, without hosting a rage peer itself.
//
// Each instance delegates its rage networking to a crecore p2p proxy (see the ocr.Proxy bootstrap
// dependency), which hosts the peer whose identity --ocr.peer-id names. So this binary needs no
// database, no keystore and no listen address of its own: it is told which peer it is and where to
// reach the process holding that peer's key, and everything else - dialling, discovery, encryption -
// happens over there.
//
// What it does with the endpoint is deliberately trivial, so that seeing the messages arrive is the
// whole point: oracle 0 says hi to 1, 1 says hi to 2, and so on round to the last, which says hi
// back to 0. Once a lap is complete 0 broadcasts that it is starting again, and the lap repeats.
// Every instance prints each message it receives.
//
// run_4.sh runs four of these against four crecore proxies on one machine.
package main

import (
	"context"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := defaultConfig

	root := &cobra.Command{
		Use:   "pingpong",
		Short: "Sample app passing messages over a proxied libocr binary network endpoint",
		Long: `Passes messages between its instances over a libocr binary network endpoint.

The endpoint is not this process's own: rage networking is delegated to the crecore p2p proxy at
--ocr.proxy-address, which hosts the peer --ocr.peer-id names. --pingpong.peers lists every
participant as peerID@host:port, in the order that decides who says hi to whom; the addresses are
where those peers listen, which is how they find each other to begin with.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "pingpong"
	if err := flags.RegisterCommandFlags(root, &cfg, opts); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root)
	lggr := bootstrapper.Logger()

	// The proxy dependency, not the host one: this binary drives an endpoint, it does not run a
	// peer. Which is also why it needs no database - it is told its peer ID rather than unlocking a
	// keystore to find it.
	ocrDep := ocr.Proxy(lggr.Named("OCR"))

	return standalone.Run1(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		factories *ocr.OCRFactories,
	) []services.Service {
		return []services.Service{newPingPong(scfg.Logger.Named("pingpong"), &cfg, factories)}
	}, ocrDep)
}

// Config is what one instance needs to know about the others. Its own place in the ring is not
// configured: it is wherever its peer ID appears in Peers.
type Config struct {
	// Each entry is peerID@host:port, because both halves are needed and there is no sense in
	// configuring them apart. The peer ID says who an oracle is - its position here is the oracle ID
	// the others address it by - and the address is where its peer listens, which libocr needs to
	// dial before any of them have heard of each other: a proxy knows only the announcements it has
	// seen, and a fresh one has seen none. The address is the crecore --ocr.listen-addresses of the
	// process hosting that peer, not its --proxy.listen-address, which is a private arrangement
	// between an app and its own proxy.
	Peers []string `usage:"peerID@host:port of every participant, in the order that decides who says hi to whom; this instance's own --ocr.peer-id must be one of them" validate:"required" example:"['12D3KooWFirst@127.0.0.1:6690','12D3KooWSecond@127.0.0.1:6691']"`

	StartDelay config.Duration `usage:"how long to wait before the first lap, giving the peers time to connect"`
	RoundDelay config.Duration `usage:"how long to wait after a lap completes before starting the next one"`
	RetryDelay config.Duration `usage:"how long oracle 0 waits for a lap to come back round before starting it again"`
}

var defaultConfig = Config{
	StartDelay: *config.MustNewDuration(5 * time.Second),
	RoundDelay: *config.MustNewDuration(2 * time.Second),
	// Long enough that a lap has really stalled rather than merely being slow, short enough that a
	// message dropped before the peers had connected does not end the demo.
	RetryDelay: *config.MustNewDuration(5 * time.Second),
}
