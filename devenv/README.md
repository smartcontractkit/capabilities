## Local Dev Environment

`devenv` defines a local developer environment for adhoc testing of capabilities.

`devenv` exposes select features of a Standard Capability over HTTP. To start the server:

```bash
$ docker compose up
```

You can now execute three requests against port `:3000`

### Get Info of Registered Capabilities

```bash
$ curl --request GET \
  --url http://localhost:3000/infos
```

### Get Info of a Registered Capability by Id

```bash
$ curl --request POST \
  --url http://localhost:3000/capability/get/info \
  --header 'Content-Type: application/json' \
  --data '{
	"id": "kv-store-target@1.0.0"
}'
```

### Call Execute on a Capability

```bash
$ curl --request POST \
  --url http://localhost:3000/capability/execute \
  --header 'Content-Type: application/json' \

  --data '{
	"id": "kv-store-target@1.0.0",
	"request": {
		"metadata": {
			"workflow_id": "some-id",
			"workflow_execution_id": "some-exec-id"
		},
		"config": {},
		"inputs": {
			"signed_report": {
				"context": [],
				"id": [],
				"report": [],
				"signatures": [
					[]
				]
			}
		}
	}
}'
```
