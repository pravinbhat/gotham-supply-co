# Roadmap

The depth belongs in Temporal concepts (determinism, versioning, retries, signals, queries, updates, schedules), not in infrastructure. Each phase ends with:

- the stack running on Temporal Cloud, and
- a "break it on purpose" exercise added to the [README](../README.md#break-it-on-purpose).

## Phase 1: Thin vertical slice ✅

- [x] Go order saga → Java billing → Python armory, happy path, over real HTTP
- [x] All three services on Temporal Cloud via envconfig (API key)
- [x] Retryable vs non-retryable error contract across HTTP
- [x] Idempotency via business-keyed workflow IDs plus database unique constraints
- [x] Synchronous HTTP facades over workflows (`USE_EXISTING` / `ALLOW_DUPLICATE_FAILED_ONLY`)
- [x] Local activities for each service's own database writes
- [x] Verified: an order survives a billing outage mid-flight; an out-of-stock order fails fast

### Known gaps from the Phase 1 review

- [ ] Billing returns `500 INTERNAL` for malformed JSON. Callers treat that as retryable, so it should be `400`.
- [ ] Armory validation errors use FastAPI's default `{"detail": ...}` body instead of `{"error", "message"}`. Callers still classify them correctly, as `HTTP_422`, which is non-retryable.
- [ ] A failed order leaves its reservation `HELD` (no compensation yet; fixed in Phase 2).
- [ ] No automated tests yet (Phase 6, but cheap workflow unit tests can start earlier).

## Phase 2: Deepen batcave-order-ops (Go)

- [ ] **Saga compensation.** Release the reservation if the charge fails; refund if shipping fails. Compensations run on a disconnected context so they still execute after cancellation.
  - Needs two small endpoints: armory `POST /reservations/{id}/release` and billing `POST /charges/{id}/refunds`. Phase 3 hardens the refund side.
- [ ] **Query** `getStatus`: live progress read from workflow state. `GET /orders/{id}` uses it first and falls back to the database.
- [ ] **Signal** `cancelOrder`: honored only before the ship step, and triggers compensation.
- [ ] **Update** `changeShippingAddress`: a validator rejects it once the order has shipped; returns the new address synchronously.
- [ ] **Manual review gate.** Orders above $X wait for an `approve` signal, racing it against a timer (`workflow.Selector` + `workflow.NewTimer`). They auto-cancel after N hours.
- [ ] **Versioning.** Add a step (e.g. a fraud check) behind `workflow.GetVersion`, and prove that histories recorded before the change still replay.
- [ ] **Search attributes:** `CustomerId`, `OrderStatus`, `OrderTotal`.

## Phase 3: Deepen wayne-billing (Java)

- [ ] A simulated "legacy gateway" with configurable latency, timeouts, and 5xx rate.
- [ ] Split authorize and capture. A declined payment becomes `PAYMENT_DECLINED`, a non-retryable business failure surfaced as `402`.
- [ ] Idempotency at the gateway boundary: what happens when the gateway succeeds but its response is lost?
- [ ] **Heartbeating.** A long settlement or batch activity reports progress in heartbeat details so a retry resumes where it left off. Set a `HeartbeatTimeout`.
- [ ] Per-activity retry policies: `setDoNotRetry`, maximum attempts, `ScheduleToClose`.
- [ ] Refunds as their own workflow, hardening the Phase 2 compensation endpoint.

## Phase 4: Deepen batcave-armory (Python)

- [ ] **Fan-out/fan-in.** `FulfillmentWorkflow` picks from each warehouse in parallel (`asyncio.gather` over activities) and handles partial failure.
- [ ] **Schedules.** A nightly `ReconciliationWorkflow` checks stock against reservations and auto-releases stale `HELD` reservations.
  - The schedule is created idempotently when the worker starts.
  - Practice pausing, triggering, and backfilling it from the CLI.
- [ ] Continue-as-new for large reconciliation runs.

## Phase 5: alfred (TypeScript)

- [ ] A long-running workflow per conversation; customer messages arrive as signals or updates.
- [ ] LLM calls and tools (order lookup, shipment tracking, refund request) run as activities.
- [ ] Escalate to a human via signal when stuck; the human's reply flows back into the conversation.
- [ ] Continue-as-new to keep history bounded on long conversations.
- [ ] Point order-ops `NotifyCustomer` at alfred.

## Phase 6: Cross-cutting

- [ ] **Worker tuning:** pollers per task queue, concurrent task slots and slot suppliers, the sticky workflow cache, and Java virtual threads.
- [ ] **Testing:**
  - time-skipping tests for timers and the review gate
  - replay tests against histories exported from Cloud (Go `WorkflowReplayer`, plus the Java and Python replayers)
  - activity unit tests
- [ ] **Observability:** SDK metrics to Prometheus/Grafana, OpenTelemetry tracing across HTTP and Temporal, and structured logs carrying workflow and run IDs.
- [ ] Worker Versioning (deployment versions) as an alternative to patching.
