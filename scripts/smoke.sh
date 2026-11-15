#!/usr/bin/env bash
# End-to-end happy path: place an order, wait for the saga to finish, show every service's view of it.
set -euo pipefail
ORDERS=${ORDERS_URL:-http://localhost:8081}
BILLING=${BILLING_URL:-http://localhost:8082}
ARMORY=${ARMORY_URL:-http://localhost:8083}

body='{
  "customer_id": "selina-kyle",
  "items": [{"sku": "BAT-001", "quantity": 2}, {"sku": "ALF-301", "quantity": 1}],
  "shipping_address": {"name": "Selina Kyle", "street": "1 Rooftop Way", "city": "Gotham", "country": "US"}
}'
echo "→ POST /orders"
resp=$(curl -sf -X POST "$ORDERS/orders" -H 'Content-Type: application/json' -d "$body")
echo "$resp"
order_id=$(echo "$resp" | python3 -c 'import sys,json;print(json.load(sys.stdin)["order_id"])')

echo "→ waiting for $order_id"
for _ in $(seq 1 60); do
  status=$(curl -sf "$ORDERS/orders/$order_id" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])' 2>/dev/null || echo PENDING)
  printf '   %s\n' "$status"
  [[ "$status" == COMPLETED || "$status" == FAILED ]] && break
  sleep 1
done

echo; echo "== order-ops ==";  curl -s "$ORDERS/orders/$order_id" | python3 -m json.tool
echo "== armory shipment =="; curl -s "$ARMORY/shipments/$order_id" | python3 -m json.tool
echo "== billing charges (latest) =="; curl -s "$BILLING/charges" | python3 -c 'import sys,json;print(json.dumps(json.load(sys.stdin)[0],indent=2))'
[[ "$status" == COMPLETED ]]
