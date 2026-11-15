# Gotham Supply Co.

> **Fan-made, non-commercial learning project.** Not affiliated with, endorsed by, or connected to DC, Warner Bros. Discovery, or any rights holder. Batman-related names are used only as playful theming; nothing is for sale.

An order fulfillment platform for an imaginary Gotham collectibles shop. It is built as four real microservices, each with its own REST API, SQLite database, and Temporal worker, and they are orchestrated with [Temporal](https://temporal.io). The aim is to learn Temporal by bolting it onto services that already talk HTTP to each other, rather than building a single-process demo.

```
                              ┌──────────────────────── Temporal Cloud ────────────────────────┐
                              │  task queues:  batcave-order-ops · wayne-billing · batcave-armory │
                              └────────▲──────────────────────▲───────────────────────▲──────────┘
                                       │ worker               │ worker                │ worker
  POST /orders ──▶ ┌───────────────────┴──┐  HTTP   ┌─────────┴──────────┐  HTTP  ┌──┴──────────────────┐
                   │ batcave-order-ops    │───────▶ │ wayne-billing      │        │ batcave-armory       │
                   │ Go · :8081           │         │ Java · :8082       │        │ Python · :8083       │
                   │ OrderWorkflow (saga) │──────────────────────────────────────▶│ reservations (txn)   │
                   │ orders.db            │         │ ChargeWorkflow     │        │ FulfillmentWorkflow  │
                   └──────────────────────┘         │ billing.db         │        │ armory.db            │
                                                    └────────────────────┘        └──────────────────────┘
```

| Service | Lang | Port | Role | Temporal it owns |
|---|---|---|---|---|
| `batcave-order-ops` | Go | 8081 | Order orchestration | `OrderWorkflow`: reserve → charge → ship → notify |
| `wayne-billing` | Java | 8082 | Payments, as "Wayne Enterprises" | `ChargeWorkflow` behind `POST /charges` |
| `batcave-armory` | Python | 8083 | Catalog, stock, fulfillment | `FulfillmentWorkflow` behind `POST /shipments` |
| `alfred` | TypeScript | 8084 | Support agent *(coming later)* | agentic loop, human escalation via signal |

## Running it

Prereqs: Go 1.23+, Java 21 + Maven, Python 3.13+ with [uv](https://docs.astral.sh/uv/), and the Temporal CLI.

```bash
cp .env.example .env      # fill in Temporal Cloud address / namespace / API key
make up                   # builds + starts all services, logs in .logs/
make smoke                # places an order and follows it to COMPLETED
make down
```

Every service connects through its SDK's **envconfig** loader, so the same `TEMPORAL_ADDRESS` / `TEMPORAL_NAMESPACE` / `TEMPORAL_API_KEY` (or `TEMPORAL_PROFILE`) work in all four languages. If none are set, everything falls back to a local `temporal server start-dev`.

Try it by hand:

```bash
curl -s localhost:8083/products | jq                     # browse the armory
curl -s -X POST localhost:8081/orders -H 'Content-Type: application/json' -d '{
  "customer_id": "selina-kyle",
  "items": [{"sku": "BAT-004", "quantity": 1}],
  "shipping_address": {"name": "Selina Kyle", "city": "Gotham", "country": "US"}}'
curl -s localhost:8081/orders/<order_id> | jq
temporal workflow show -w order-<order_id>                # the event history
```

Things worth breaking on purpose:

- **Kill wayne-billing mid-order** (`kill $(cat .run/wayne-billing.pid)`). The order parks in `CHARGING_PAYMENT` while `ChargePayment` retries with backoff. Run `make up` and it completes on its own.
- **Order more than exists** (`BAT-003` has only 2 in stock). The armory returns `409 OUT_OF_STOCK`, the activity turns it into a *non-retryable* `ApplicationError`, and the order fails right away instead of retrying.
- **Resubmit an `order_id`**. You get `409 ORDER_EXISTS` (workflow ID reuse policy).
- **Replay a charge** with the same `Idempotency-Key`. You get the same charge back. Send a different amount and you get `422 IDEMPOTENCY_KEY_REUSED`.

## Design notes

**Cross-service calls are real HTTP.** The order saga's activities call the other services' public REST APIs. They don't use child workflows or shared task queues. Each service is a black box with its own DB, which is the usual situation when Temporal is introduced into an existing estate.

**One error contract, everywhere.** Error bodies are `{"error": CODE, "message": ...}`. In `postJSON` ([activities.go](services/batcave-order-ops/internal/activities/activities.go)):

- Network failures, 5xx, 408 and 429 are retryable.
- Any other 4xx becomes a non-retryable `ApplicationError` whose `Type` is the remote code, so the workflow can branch on `OUT_OF_STOCK`, `PAYMENT_DECLINED` and so on.

**Idempotency comes from business IDs.** Each layer keys its writes to the order:

- **Order workflow:** ID is `order-<order_id>`, with reuse policy `REJECT_DUPLICATE`.
- **Billing:** `Idempotency-Key: order-<order_id>` maps to workflow `charge-<key>`.
  - Conflict policy `USE_EXISTING`: concurrent duplicates attach to the running charge.
  - Reuse policy `ALLOW_DUPLICATE_FAILED_ONLY`: a failed run can be retried, a completed charge is final.
  - The DB has a `UNIQUE(idempotency_key)` constraint.
- **Armory:** reservations and shipments are `UNIQUE(order_id)`, and fulfillment uses workflow `fulfill-<order_id>`.

Activity retries can't double-charge or double-ship.

**Sync HTTP facades over workflows.** `POST /charges` and `POST /shipments` start a workflow and wait for its result for at most 20s. That is shorter than the caller's 25s HTTP timeout and 30s activity `StartToClose`. If it takes longer, they return `504 …_IN_PROGRESS`. The caller's activity retries with the same key and attaches to the same execution.

**Local activities for local writes.** `OrderWorkflow` records each status transition in its own SQLite DB as a *local* activity. That avoids a task-queue round trip, and the result appears in history as `MarkerRecorded` events.

## Repo layout

```
services/
  batcave-order-ops/   Go     cmd/server · internal/{workflows,activities,api,store,shared}
  wayne-billing/       Java   com.gotham.billing.{workflow,activities,api,db,model}
  batcave-armory/      Python src/batcave_armory/{workflows,activities,api,db,models}.py
scripts/dev.sh         start/stop/status/logs for all services
scripts/smoke.sh       end-to-end happy path
docs/ROADMAP.md        phased build plan + Temporal concepts per phase
```
