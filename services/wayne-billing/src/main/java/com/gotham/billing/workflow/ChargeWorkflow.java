package com.gotham.billing.workflow;

import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import io.temporal.workflow.WorkflowInterface;
import io.temporal.workflow.WorkflowMethod;

@WorkflowInterface
public interface ChargeWorkflow {
  String TASK_QUEUE = "wayne-billing";

  /** Workflow ID is derived from the idempotency key — that's the dedup mechanism. */
  static String workflowId(String idempotencyKey) {
    return "charge-" + idempotencyKey;
  }

  @WorkflowMethod
  Charge charge(ChargeCommand command);
}
