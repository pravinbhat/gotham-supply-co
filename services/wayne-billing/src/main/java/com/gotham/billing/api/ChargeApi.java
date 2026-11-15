package com.gotham.billing.api;

import com.gotham.billing.db.ChargeStore;
import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import com.gotham.billing.model.ChargeRequest;
import com.gotham.billing.workflow.ChargeWorkflow;
import io.javalin.Javalin;
import io.javalin.http.Context;
import io.temporal.api.enums.v1.WorkflowIdConflictPolicy;
import io.temporal.api.enums.v1.WorkflowIdReusePolicy;
import io.temporal.client.WorkflowClient;
import io.temporal.client.WorkflowExecutionAlreadyStarted;
import io.temporal.client.WorkflowFailedException;
import io.temporal.client.WorkflowOptions;
import io.temporal.failure.ApplicationFailure;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * REST surface. POST /charges is a synchronous facade over ChargeWorkflow: callers see a plain
 * HTTP API, but inside billing the charge is a durable workflow keyed by the idempotency key.
 */
public class ChargeApi {
  private static final Logger log = LoggerFactory.getLogger(ChargeApi.class);
  /** Must stay below the caller's HTTP/activity timeout so we answer before they give up. */
  private static final long RESULT_WAIT_SECONDS = 20;

  private final WorkflowClient client;
  private final ChargeStore store;

  public ChargeApi(WorkflowClient client, ChargeStore store) {
    this.client = client;
    this.store = store;
  }

  public void register(Javalin app) {
    app.get("/healthz", ctx -> ctx.json(Map.of("status", "ok")));
    app.post("/charges", this::createCharge);
    app.get("/charges", ctx -> ctx.json(store.list(50)));
    app.get(
        "/charges/{id}",
        ctx ->
            store
                .findById(ctx.pathParam("id"))
                .ifPresentOrElse(ctx::json, () -> error(ctx, 404, "NOT_FOUND", "no such charge")));
  }

  private void createCharge(Context ctx) throws Exception {
    String key = ctx.header("Idempotency-Key");
    if (key == null || key.isBlank()) {
      error(ctx, 400, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required");
      return;
    }
    ChargeRequest req = ctx.bodyAsClass(ChargeRequest.class);
    if (req.orderId() == null || req.amountCents() <= 0) {
      error(ctx, 400, "INVALID_REQUEST", "order_id and a positive amount_cents are required");
      return;
    }

    // Fast path: this key already produced a charge.
    Optional<Charge> existing = store.findByIdempotencyKey(key);
    if (existing.isPresent()) {
      if (existing.get().amountCents() != req.amountCents()
          || !existing.get().orderId().equals(req.orderId())) {
        error(ctx, 422, "IDEMPOTENCY_KEY_REUSED", "key was used for a different charge");
        return;
      }
      ctx.json(existing.get());
      return;
    }

    String workflowId = ChargeWorkflow.workflowId(key);
    ChargeWorkflow wf =
        client.newWorkflowStub(
            ChargeWorkflow.class,
            WorkflowOptions.newBuilder()
                .setWorkflowId(workflowId)
                .setTaskQueue(ChargeWorkflow.TASK_QUEUE)
                // Concurrent duplicate request while running -> attach to the same execution.
                .setWorkflowIdConflictPolicy(
                    WorkflowIdConflictPolicy.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING)
                // A completed charge is final; only a technically *failed* run may be retried.
                .setWorkflowIdReusePolicy(
                    WorkflowIdReusePolicy.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY)
                .build());
    ChargeCommand cmd =
        new ChargeCommand(
            key,
            req.orderId(),
            req.customerId(),
            req.amountCents(),
            req.currency() == null ? "USD" : req.currency());
    try {
      WorkflowClient.start(wf::charge, cmd);
    } catch (WorkflowExecutionAlreadyStarted e) {
      log.info("charge workflow {} already exists; returning its result", workflowId);
    }

    try {
      Charge charge =
          client
              .newUntypedWorkflowStub(workflowId, Optional.empty(), Optional.empty())
              .getResult(RESULT_WAIT_SECONDS, TimeUnit.SECONDS, Charge.class);
      ctx.json(charge);
    } catch (TimeoutException e) {
      // The charge is still in flight (durably). Caller retries with the same key and attaches.
      error(ctx, 504, "CHARGE_IN_PROGRESS", "charge still processing; retry with the same key");
    } catch (WorkflowFailedException e) {
      if (e.getCause() instanceof ApplicationFailure af && af.isNonRetryable()) {
        error(ctx, 402, af.getType(), af.getOriginalMessage());
      } else {
        error(ctx, 502, "CHARGE_FAILED", String.valueOf(e.getCause()));
      }
    }
  }

  static void error(Context ctx, int status, String code, String message) {
    ctx.status(status).json(Map.of("error", code, "message", message == null ? "" : message));
  }
}
