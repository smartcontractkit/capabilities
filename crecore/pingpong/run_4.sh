#!/usr/bin/env bash
#
# Runs four crecore p2p proxies and four pingpong instances against them, on this machine.
#
# Each proxy hosts one peer, so each needs a keystore of its own to take that peer's identity from -
# crecore reads the first key ring in the database it is pointed at. That means four databases: one
# shared one would hand every proxy the same key, and four proxies claiming one peer ID is not a
# network. They are created here, seeded by ./setup, and dropped again on the next run.
#
# Because each proxy then keeps its own record of the announcements it has seen, a fresh one knows
# of nobody. The apps are configured with peerID@address for every peer, which is how each proxy is
# told where the others listen.
#
# Needs a postgres to create databases in. Point DATABASE_URL at one; the default is a local server
# with the usual postgres/postgres credentials, e.g.
#
#   docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres --name cre-pingpong-db postgres:16
#
# Ctrl-C stops everything.
set -euo pipefail

cd "$(dirname "$0")"

INSTANCES=${INSTANCES:-4}
DATABASE_URL=${DATABASE_URL:-postgresql://postgres:postgres@localhost:5432/postgres?sslmode=disable}
# One password for every shard: they are four halves of one demo, not four operators.
KEYSTORE_PASSWORD=${KEYSTORE_PASSWORD:-pingpong-keystore-password}

DB_PREFIX=${DB_PREFIX:-cre_pingpong}
P2P_PORT=${P2P_PORT:-6690}        # what each proxy's rage peer listens on
PROXY_PORT=${PROXY_PORT:-50051}   # what each proxy serves its gRPC on, and what its app dials
HEALTH_PORT=${HEALTH_PORT:-9100}  # /healthz and /metrics, one port per proxy

# The registry crecore serves alongside the proxy always runs and wants an address and an RPC. There
# is no chain here, so it is pointed at a dead one: it logs that it cannot sync, and the proxy - the
# part this demo uses - is unaffected.
REGISTRY_ADDRESS=${REGISTRY_ADDRESS:-0x0000000000000000000000000000000000000001}
EVM_HTTP_URL=${EVM_HTTP_URL:-http://127.0.0.1:1}
EVM_CHAIN_ID=${EVM_CHAIN_ID:-1}

BIN=$(mktemp -d)
LOGS=${LOGS:-$(mktemp -d)}
pids=()

cleanup() {
	trap - INT TERM EXIT
	echo
	echo "stopping..."
	for pid in "${pids[@]:-}"; do
		[[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
	done
	wait 2>/dev/null || true
	rm -rf "$BIN"
}
trap cleanup INT TERM EXIT

# psql_admin runs SQL against the server itself rather than one of the demo databases, for creating
# and dropping them.
psql_admin() { psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "$1"; }

# database_url returns the url of instance $1's database, keeping the credentials and options of
# DATABASE_URL and replacing only the database name.
database_url() {
	python3 - "$DATABASE_URL" "${DB_PREFIX}_$1" <<-'EOF'
		import sys
		from urllib.parse import urlsplit, urlunsplit
		url, name = sys.argv[1], sys.argv[2]
		parts = urlsplit(url)
		print(urlunsplit(parts._replace(path="/" + name)))
	EOF
}

echo "building..."
go build -o "$BIN/crecore" ..
go build -o "$BIN/pingpong" .
go build -o "$BIN/setup" ./setup

echo "preparing $INSTANCES databases and keystores..."
peers=()
for i in $(seq 0 $((INSTANCES - 1))); do
	psql_admin "DROP DATABASE IF EXISTS ${DB_PREFIX}_$i"
	psql_admin "CREATE DATABASE ${DB_PREFIX}_$i"
	peer_id=$("$BIN/setup" -url "$(database_url "$i")" -password "$KEYSTORE_PASSWORD")
	# Where instance i's peer lives: its ID, and the address the proxy hosting it listens on.
	peers+=("$peer_id@127.0.0.1:$((P2P_PORT + i))")
done

# Every peer in one comma-separated list: a peer's position here is the oracle ID the others address
# it by, so instance i is oracle i.
peer_list=$(IFS=,; echo "${peers[*]}")

echo "starting $INSTANCES proxies..."
for i in $(seq 0 $((INSTANCES - 1))); do
	"$BIN/crecore" run \
		--ocr.listen-addresses="127.0.0.1:$((P2P_PORT + i))" \
		--ocr.keystore-password="$KEYSTORE_PASSWORD" \
		--database.url="$(database_url "$i")" \
		--grpc.host="127.0.0.1" \
		--grpc.port="$((PROXY_PORT + i))" \
		--http.port="$((HEALTH_PORT + i))" \
		--capabilities-registry.address="$REGISTRY_ADDRESS" \
		--evm.chain-id="$EVM_CHAIN_ID" \
		--evm.http-url="$EVM_HTTP_URL" \
		>"$LOGS/crecore_$i.log" 2>&1 &
	pids+=($!)
done

# Long enough for each proxy to have opened its database, unlocked its key and bound its port; the
# apps retry anyway, so this only keeps the first few seconds of output tidy.
sleep 5

echo "starting $INSTANCES apps..."
for i in $(seq 0 $((INSTANCES - 1))); do
	"$BIN/pingpong" run \
		--ocr.proxy-address="127.0.0.1:$((PROXY_PORT + i))" \
		--ocr.peer-id="${peers[$i]%@*}" \
		--http.port=""$((HEALTH_PORT + i + INSTANCES))"" \
		--pingpong.peers="$peer_list" \
		2>"$LOGS/pingpong_$i.log" &
	pids+=($!)
done

echo
echo "logs are in $LOGS; the messages below are the apps' stdout"
echo
wait
