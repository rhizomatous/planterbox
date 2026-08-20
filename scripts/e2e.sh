#!/usr/bin/env bash
# End to end against a real container runtime. Egress, so far.
#
# The unit tests are pure and pin the arguments plbx builds. They cannot show
# that a sandbox actually reaches the proxy, which is the half that depends on
# the runtime: whether the relay resolves the host, and whether one sandbox's
# lifecycle leaves another's alone.
#
# Run it on Linux too. host.docker.internal is a Docker Desktop convenience
# that Docker Engine does not publish, so the Linux run is what proves the
# --add-host mapping does its job; a pure test can only pin the flag.
#
# Uses an isolated daemon with its own state directory, runtime directory and
# proxy port, so it leaves what you already have alone.
set -uo pipefail

cd "$(dirname "$0")/.."

ROOT=$(mktemp -d /tmp/plbxlive.XXXXXX)
export PLBX_STATE_DIR="$ROOT/state" PLBX_RUNTIME_DIR="$ROOT/run"
PROXY=127.0.0.1:47931
A=livetest-a
B=livetest-b
PASS=0 FAIL=0

ok()   { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

cleanup() {
  ./plbx rm --force $A $B >/dev/null 2>&1
  [ -n "${DAEMON:-}" ] && kill "$DAEMON" 2>/dev/null
  rm -rf "$ROOT"
}
trap cleanup EXIT

# only this run's containers: a legacy shared plbx-relay may predate it
containers() { docker ps -a --format '{{.Names}}' | grep -E '^plbx-livetest' | sort | tr '\n' ' '; }
egress()     { ./plbx exec "$1" bash -lc 'curl -s -o /dev/null -w "%{http_code}" --max-time 20 https://github.com' 2>/dev/null | tail -1; }

echo "building..."
V=livetest
go build -ldflags "-X main.version=$V" -o plbx ./cmd/plbx &&
go build -ldflags "-X main.version=$V" -o plbxd ./cmd/plbxd || exit 1

./plbxd --proxy $PROXY >"$ROOT/daemon.log" 2>&1 &
DAEMON=$!
for _ in $(seq 1 50); do [ -S "$ROOT/run/plbxd.sock" ] && break; sleep 0.2; done
[ -S "$ROOT/run/plbxd.sock" ] || { echo "daemon never came up:"; cat "$ROOT/daemon.log"; exit 1; }

echo; echo "== two sandboxes, each with its own relay =="
./plbx create shell /tmp --name $A >/dev/null && ./plbx create shell /tmp --name $B >/dev/null || exit 1
./plbx start $A >/dev/null && ./plbx start $B >/dev/null || exit 1
check "each sandbox has its own relay" "$(containers)" "plbx-livetest-a plbx-livetest-a-relay plbx-livetest-b plbx-livetest-b-relay "

echo; echo "== egress works through each =="
check "$A reaches an allowed host" "$(egress $A)" "200"
check "$B reaches an allowed host" "$(egress $B)" "200"

echo; echo "== one sandbox's lifecycle leaves the other alone =="
./plbx stop $A >/dev/null
check "stopping $A took only its relay" "$(containers)" "plbx-livetest-a plbx-livetest-b plbx-livetest-b-relay "
check "$B still reaches an allowed host" "$(egress $B)" "200"

echo; echo "== restarting one does not disturb the other =="
./plbx start $A >/dev/null
check "$A reaches it again" "$(egress $A)" "200"
check "$B is unaffected" "$(egress $B)" "200"

echo; echo "== a relay lost by hand is rebuilt without stranding anyone =="
# the failure the shared relay had: rebuilding it re-attached it to whichever
# sandbox triggered the rebuild, and cut every other running one off.
docker rm --force "$(docker ps -a --format '{{.Names}}' | grep -E '^plbx-livetest-a-relay$|^plbx-relay$' | head -1)" >/dev/null 2>&1
./plbx stop $A >/dev/null && ./plbx start $A >/dev/null
check "$A recovers" "$(egress $A)" "200"
check "$B was not cut off by $A rebuilding" "$(egress $B)" "200"

echo; echo "== nothing outlives the sandboxes =="
./plbx rm --force $A $B >/dev/null
check "no container left behind" "$(containers)" ""
# a relay that serves everyone has to outlive any one of them, so nothing owns
# the moment it should go
check "no relay outlives the last sandbox" "$(docker ps -a --format '{{.Names}}' | grep -cx 'plbx-relay')" "0"

echo; printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
