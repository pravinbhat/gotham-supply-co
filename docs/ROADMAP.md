# Roadmap

The depth belongs in Temporal concepts, not in infrastructure. Each phase should end with the stack running on Temporal Cloud and a "break it on purpose" exercise.

## Phase 1: Thin vertical slice ✅

- [x] Go order saga → Java billing → Python armory, happy path, over real HTTP
- [x] All three services on Temporal Cloud via envconfig (API key)
- [x] Retryable vs non-retryable error contract across HTTP
- [x] Idempotency via business-keyed workflow IDs plus DB unique constraints
- [x] Sync HTTP facades over workflows (`USE_EXISTING` / `ALLOW_DUPLICATE_FAILED_ONLY`)
- [x] Local activities for the service's own DB writes
- [x] Verified: survives a billing outage mid-order; out-of-stock fails fast

## Phase 2: Deepen batcave-order-ops (Go)

- [ ] **Saga compensation**: release the reservation if charge fails, refund if ship fails (needs `POST /reservations/{id}/release` and `POST /charges/{id}/refunds`); compensations run on a disconnected context
- [ ] **Query** `getStatus`: live progress from workflow state; `GET /orders/{id}` prefers it and falls back to the DB
- [ ] **Signal** `cancelOrder`: honored before the ship step, triggers compensation
- [ ] **Update** `changeShippingAddress`: validator rejects it once shipped; returns the new address synchronously
- [ ] **Manual review gate**: orders above $X wait for an `approve` signal with a timer (`workflow.Selector` + `NewTimer`) and auto-cancel after N hours
- [ ] **Versioning**: add a step (e.g. a fraud check) with `workflow.GetVersion`; prove old histories still replay
- [ ] Search attributes: `CustomerId`, `OrderStatus`, `OrderTotal`

## Phase 3: Deepen wayne-billing (Java)

- [ ] Simulated "legacy gateway" with configurable latency, timeouts and 5xx rate
- [ ] Split authorize and capture; `PAYMENT_DECLINED` as a non-retryable business failure (402)
- [ ] Idempotency at the gateway boundary: what happens when the gateway succeeds but the response is lost?
- [ ] **Heartbeating**: long settlement/batch activity with heartbeat details for resumable progress; `HeartbeatTimeout`
- [ ] Tuned retry policies per activity (`setDoNotRetry`, max attempts, `ScheduleToClose`)
- [ ] Refunds as their own workflow (the saga compensation target)

## Phase 4: Deepen batcave-armory (Python)

- [ ] **Fan-out/fan-in**: `FulfillmentWorkflow` picks per warehouse in parallel (`asyncio.gather` over activities); partial-failure handling
- [ ] **Schedules**: nightly `ReconciliationWorkflow` (stock vs reservations, stale HELD reservations auto-released), created idempotently at worker start; pause, trigger and backfill from the CLI
- [ ] Continue-as-new for large reconciliation runs

## Phase 5: alfred (TypeScript)

- [ ] Long-running per-conversation workflow; customer messages arrive as signals or updates
- [ ] LLM calls and tools (order lookup, shipment tracking, refund request) as activities
- [ ] Escalate to a human via signal when stuck; human reply flows back in
- [ ] Continue-as-new to bound history on long conversations
- [ ] order-ops `NotifyCustomer` → alfred

## Phase 6: Cross-cutting

- [ ] Worker tuning: task-queue pollers, slot suppliers, `maxConcurrentActivityExecutionSize`, sticky cache; Java virtual threads
- [ ] Testing: time-skipping tests for timers and review gates; replay tests against histories exported from Cloud (Go `WorkflowReplayer`, Java/Python replayers); activity unit tests
- [ ] Observability: SDK metrics → Prometheus/Grafana, OpenTelemetry tracing across HTTP plus Temporal, structured logs with workflow/run IDs
- [ ] Worker Versioning (deployment versions) as an alternative to patching
