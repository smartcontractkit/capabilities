// Command testtrigger fires an HTTP trigger, the way a customer's tooling would.
//
// It exists because a trigger request has to be signed - a gateway runs a
// workflow for whoever the workflow authorised, and proving that means a token
// over the request's digest, which curl cannot make. It is a test tool: the key
// it signs with is a constant, so that the address to authorise is written down
// once (see README.md) rather than minted fresh every run.
//
// It is not part of the http binary. Firing a trigger is the customer's side of
// the gateway, and the binary that serves capabilities has no business doing it.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/google/uuid"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/capabilities/http/gateway/connector"
	"github.com/smartcontractkit/capabilities/http/gateway/jwt"
)

// key is what this tool signs as, and it is a constant on purpose: subscribing to
// a trigger means naming the addresses allowed to fire it, and a key invented per
// run would mean re-subscribing every time. It is a test key. Nothing it can sign
// is worth anything, and it is in a public repository.
//
// Address is the account it signs as, which is the value to authorise. It is
// checked against the key at startup, so the two cannot drift apart.
const (
	key     = "0000000000000000000000000000000000000000000000000000000000000001"
	Address = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
)

// lifetime is how long the token this signs is good for. A gateway allows at most
// five minutes and the request is sent immediately, so there is nothing here for
// a caller to choose.
const lifetime = time.Minute

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port     = flag.Int("port", 0, "the --http.port an embedded run was started with; its gateway is served there")
		url      = flag.String("url", "", "gateway to send the request to, in full, when it is not an embedded run's")
		workflow = flag.String("workflow-id", "", "the workflow to run, as the subscription reports it (required)")
		body     = flag.String("body", "", "the JSON the workflow is run with; -file reads it from a file instead")
		file     = flag.String("file", "", "file to read the trigger's input from, instead of -body")
	)
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: the input goes in -body or -file", flag.Arg(0))
	}

	where, err := target(*url, *port)
	if err != nil {
		return err
	}
	id, err := workflowID(*workflow)
	if err != nil {
		return err
	}

	input, err := input(*body, *file)
	if err != nil {
		return err
	}

	signer := secp256k1.PrivKeyFromBytes(decode(key))
	account, err := jwt.Address(signer)
	if err != nil {
		return err
	}
	if account != Address {
		return fmt.Errorf("the address written down here is %s, but the key signs as %s", Address, account)
	}

	params, err := json.Marshal(gateway.HTTPTriggerRequest{
		Input:    input,
		Key:      gateway.AuthorizedKey{KeyType: gateway.KeyTypeECDSAEVM, PublicKey: account},
		Workflow: gateway.WorkflowSelector{WorkflowID: id},
	})
	if err != nil {
		return fmt.Errorf("failed to encode the request: %w", err)
	}

	raw := json.RawMessage(params)
	request := jsonrpc.Request[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      uuid.NewString(),
		Method:  gateway.MethodWorkflowExecute,
		Params:  &raw,
	}

	// The token's ID is what a gateway refuses a second use of, so it is fresh per
	// request rather than per key - which is what lets one constant key fire as
	// often as a tester likes.
	token, err := jwt.Issue(signer, uuid.NewString(), request, lifetime)
	if err != nil {
		return err
	}
	request.Auth = token

	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to encode the request: %w", err)
	}

	answer, err := post(context.Background(), where, encoded)
	if err != nil {
		return err
	}
	fmt.Println(answer)
	return nil
}

// target is where the request goes: an embedded run's gateway, on the port that
// run was started with, or a URL for a gateway that is somebody else's.
//
// The port is required for the same reason --http.port is required of the binary
// itself: there is no default port to run on, so there is no default port to
// trigger at either.
func target(url string, port int) (string, error) {
	switch {
	case url != "" && port != 0:
		return "", errors.New("say where once: -port for an embedded run, or -url for a gateway elsewhere")
	case url != "":
		return url, nil
	case port != 0:
		return fmt.Sprintf("http://localhost:%d%s", port, connector.EmbeddedGatewayPath), nil
	}
	return "", errors.New("say where to send it: -port, the --http.port of an embedded run, or -url for a gateway elsewhere")
}

// workflowID is the workflow to run, as the DON holds it: 0x-prefixed and lower
// case. The debug UI shows it without the prefix, so a value copied from there is
// accepted as it is rather than rejected for a detail the reader did not choose.
func workflowID(value string) (string, error) {
	if value == "" {
		return "", errors.New("say which workflow to run: -workflow-id, the workflow the subscription shows")
	}

	id := strings.ToLower(strings.TrimPrefix(value, "0x"))
	if len(id) != 64 {
		return "", fmt.Errorf("a workflow ID is 32 bytes of hex, not %d characters: %s", len(id), value)
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", fmt.Errorf("the workflow ID is not hex: %s", value)
	}
	return "0x" + id, nil
}

// input is what the workflow is run with: -body, or the contents of -file.
//
// One of them, and not both: a trigger with no input is a request that says
// nothing, and a run that took the input from somewhere other than where the
// caller thought is worse than being asked to say which.
func input(body, file string) (json.RawMessage, error) {
	var raw []byte
	switch {
	case body != "" && file != "":
		return nil, errors.New("give the input once: -body, or -file")
	case body != "":
		raw = []byte(body)
	case file != "":
		read, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read the input: %w", err)
		}
		raw = read
	default:
		return nil, errors.New("give the input: -body '{\"json\":\"here\"}', or -file")
	}

	// Checked here rather than left to the gateway: an input that is not JSON is a
	// typo in a shell quote, and finding that out from a signature-checked round
	// trip is a slow way to be told.
	if !json.Valid(raw) {
		return nil, fmt.Errorf("the input is not JSON: %s", strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// decode reads the constant key. It cannot fail: the key is a literal above, and
// a change to it that is not hex is a compile-time mistake caught by the first
// run rather than a case to handle.
func decode(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		panic("the key in this file is not a 32-byte hex string")
	}
	return decoded
}

func post(ctx context.Context, url string, body []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("failed to reach the gateway: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	answer, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read the gateway's answer: %w", err)
	}
	return strings.TrimSpace(string(answer)), nil
}

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), `testtrigger fires one HTTP trigger request at a gateway, signed as a customer.

  testtrigger -port <http.port> -workflow-id <id> -body '{"json":"input"}'

The port is the --http.port an embedded run was started with, which is where its
gateway is served; -url takes its place for a gateway that is not one of those.
The workflow ID is the one the subscription was registered on: the debug UI shows
it beside the trigger ID of an open subscription.

The input is -body, or the contents of -file. One of the two is required.

It signs as `+Address+`, which is the address to
authorise when subscribing. See README.md.

Flags:
`)
	flag.PrintDefaults()
}
