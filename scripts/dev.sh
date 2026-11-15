#!/usr/bin/env bash
# Start/stop all Gotham services locally. Logs -> .logs/<svc>.log, PIDs -> .run/<svc>.pid
#   scripts/dev.sh up | down | status | logs <svc>
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p .logs .run data

# Load .env if present (values already in the shell win).
if [[ -f .env ]]; then
  while IFS='=' read -r k v; do
    [[ -z "$k" || "$k" == \#* ]] && continue
    [[ -z "${!k:-}" ]] && export "$k=$v"
  done < .env
fi

SERVICES=(batcave-armory wayne-billing batcave-order-ops)
port_of() { case $1 in batcave-order-ops) echo 8081;; wayne-billing) echo 8082;; batcave-armory) echo 8083;; alfred) echo 8084;; esac; }

start() {
  local svc=$1 port; port=$(port_of "$svc")
  if [[ -f .run/$svc.pid ]] && kill -0 "$(cat .run/$svc.pid)" 2>/dev/null; then
    echo "  $svc already running (pid $(cat .run/$svc.pid))"; return
  fi
  case $svc in
    batcave-order-ops)
      (cd services/batcave-order-ops && go build -o bin/order-ops ./cmd/server)
      DB_PATH="$ROOT/data/order-ops.db" PORT=$port nohup services/batcave-order-ops/bin/order-ops >".logs/$svc.log" 2>&1 & ;;
    wayne-billing)
      local jar=services/wayne-billing/target/wayne-billing.jar
      # Rebuild only when the jar is missing or any source/pom is newer (mvn package is slow).
      if [[ ! -f $jar || -n "$(find services/wayne-billing/src services/wayne-billing/pom.xml -newer "$jar" -print -quit)" ]]; then
        echo "  building wayne-billing..."; (cd services/wayne-billing && mvn -q -DskipTests package)
      fi
      DB_PATH="$ROOT/data/billing.db" PORT=$port nohup java -jar services/wayne-billing/target/wayne-billing.jar >".logs/$svc.log" 2>&1 & ;;
    batcave-armory)
      (cd services/batcave-armory && uv sync -q)
      DB_PATH="$ROOT/data/armory.db" PORT=$port nohup services/batcave-armory/.venv/bin/batcave-armory >".logs/$svc.log" 2>&1 & ;;
  esac
  echo $! > ".run/$svc.pid"
  for _ in $(seq 1 60); do
    curl -sf "localhost:$port/healthz" >/dev/null 2>&1 && { echo "  ✓ $svc on :$port"; return; }
    kill -0 "$(cat .run/$svc.pid)" 2>/dev/null || { echo "  ✗ $svc exited — see .logs/$svc.log"; tail -20 ".logs/$svc.log"; return 1; }
    sleep 0.5
  done
  echo "  ✗ $svc did not become healthy — see .logs/$svc.log"; return 1
}

stop() {
  local svc=$1
  if [[ -f .run/$svc.pid ]]; then
    kill "$(cat .run/$svc.pid)" 2>/dev/null || true
    rm -f ".run/$svc.pid"; echo "  stopped $svc"
  fi
}

case "${1:-up}" in
  up)     echo "Temporal: ${TEMPORAL_ADDRESS:-localhost:7233 (dev server)} ns=${TEMPORAL_NAMESPACE:-default}"
          for s in "${SERVICES[@]}"; do start "$s"; done ;;
  down)   for s in "${SERVICES[@]}"; do stop "$s"; done ;;
  status) for s in "${SERVICES[@]}"; do
            p=$(port_of "$s"); curl -sf "localhost:$p/healthz" >/dev/null 2>&1 && echo "  ✓ $s :$p" || echo "  ✗ $s :$p"; done ;;
  logs)   tail -f ".logs/${2:?service name}.log" ;;
  *)      echo "usage: $0 up|down|status|logs <svc>"; exit 2 ;;
esac
