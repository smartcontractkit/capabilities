# testtrigger

Fires one HTTP trigger request at a gateway, signed the way a customer's tooling
signs one.

A gateway runs a workflow for whoever the workflow authorised, and proving that
means a JWT over the request's digest signed with an Ethereum key — which is why
this exists rather than a curl line.

## The key

It signs with a constant key, so that the address a subscription authorises is
written down once instead of being minted fresh every run:

| | |
|-|-|
| private key | `0000000000000000000000000000000000000000000000000000000000000001` |
| address | `0x7e5f4552091a69125d5dfcb7b8c2659029395bdf` |

It is a test key. It is in a public repository, it holds nothing, and nothing it
signs is worth anything.

## Firing one

Start an embedded run, which serves its own gateway (see the `http` binary's
`embed`):

```sh
go run . embed --instances 4 \
  --database.url "postgresql://$USER@localhost:5432/http_embed_test?sslmode=disable" \
  --http.port=51200
```

Subscribe to the trigger on the debug UI at
`http://localhost:51200/debug/capabilities/request`:

- method `capabilities.networking.http.v1alpha.HTTPSubscriptions.Trigger`
- select every instance
- authorized keys: one entry, type `KEY_TYPE_ECDSA_EVM`, public key
  `0x7e5f4552091a69125d5dfcb7b8c2659029395bdf`


Keep the page open: a subscription nothing is reading closes after about a
minute, and a closed one is not triggerable.

Then fire it, naming that run's port and that workflow:

```sh
go run ./testtrigger -port 51200 -workflow-id 7569… -body '{"hello":"world"}'
go run ./testtrigger -port 51200 -workflow-id 7569… -file input.json
```

To fire at the same workflow across page reloads, fill **WorkflowID** and
**WorkflowName** under Advanced before subscribing: a field that is filled in is
used as it is, and only a blank one is invented.

`-port` is the `--http.port` the run was started with, which is where its gateway
is served. Both it and `-workflow-id` are required: there is no default port to
run on, so there is none to trigger at, and the workflow to run is whichever one
you subscribed.

The input is `-body`, or the contents of `-file`: one of the two is required, and
naming both is an error.

The answer is the gateway's:

```json
{"jsonrpc":"2.0","id":"…","result":{"workflow_id":"0x7569…","workflow_execution_id":"0x…","status":"ACCEPTED"}}
```

and the payload appears on the subscription in the UI.

## Against something other than an embedded run

`-url` takes the place of `-port` when the gateway is not an embedded run's. An
embedded run serves its gateway on its own HTTP port under `/gateway`; a deployed
gateway serves customers on a listener of its own:

```sh
go run ./testtrigger -url http://localhost:5012/ -workflow-id 0x… -body '{"hello":"world"}'
```
