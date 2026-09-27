#!/usr/bin/env bash
# Demo: Z3 alone on the full graph times out; the swarm closes the gap with
# small verified certificates. Watch it at http://localhost:8080.
#
#   scripts/demo.sh                      # dsjc125.5, 16 agents
#   GRAPH=instances/dsjc250.5.col scripts/demo.sh
#   AGENTS="-haiku 16 -sonnet 3 -opus 1 -heuristic 4" scripts/demo.sh
set -euo pipefail
cd "$(dirname "$0")/.."

GRAPH=${GRAPH:-instances/dsjc125.5.col}
ADDR=${ADDR:-:8080}
SOLO_K=${SOLO_K:-13}
SOLO_TIMEOUT=${SOLO_TIMEOUT:-30s}
LOG=${LOG:-}
URL="http://localhost${ADDR}"

if [[ -z "${AGENTS:-}" ]]; then
  if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    AGENTS="-haiku 16 -sonnet 3 -opus 1 -heuristic 4"
  else
    AGENTS="-heuristic 16"
  fi
fi

command -v z3 >/dev/null || echo "note: z3 not found (brew install z3); the server will use its native solver"

echo "==> building"
go build -o bin/ ./cmd/...

echo
echo "==> step 1: hand the WHOLE graph to Z3 and ask it to prove chi >= ${SOLO_K}"
./bin/swarm-solo -graph "$GRAPH" -k "$SOLO_K" -timeout "$SOLO_TIMEOUT" || true

echo
echo "==> step 2: start the swarm server"
ulimit -n 10240 2>/dev/null || true
./bin/swarm-server -graph "$GRAPH" -addr "$ADDR" ${LOG:+-log "$LOG"} &
SERVER=$!
trap 'kill $SERVER 2>/dev/null' EXIT
sleep 1
echo "    dashboard: $URL"
(command -v open >/dev/null && open "$URL") || true

echo
echo "==> step 3: launch the swarm ($AGENTS)"
./bin/swarm-agent -server "$URL" $AGENTS
