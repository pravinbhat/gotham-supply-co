package com.gotham.billing.workflow;

import com.gotham.billing.activities.BillingActivities;
import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import io.temporal.activity.ActivityOptions;
import io.temporal.common.RetryOptions;
import io.temporal.workflow.Workflow;
import java.time.Duration;
import org.slf4j.Logger;

/** Slice 1: a single capture step. Phase 3 adds the flaky legacy gateway, auth/capture split, heartbeats. */
public class ChargeWorkflowImpl implements ChargeWorkflow {
  private static final Logger log = Workflow.getLogger(ChargeWorkflowImpl.class);

  private final BillingActivities activities =
      Workflow.newActivityStub(
          BillingActivities.class,
          ActivityOptions.newBuilder()
              .setStartToCloseTimeout(Duration.ofSeconds(10))
              .setRetryOptions(
                  RetryOptions.newBuilder()
                      .setInitialInterval(Duration.ofMillis(500))
                      .setMaximumInterval(Duration.ofSeconds(10))
                      .build())
              .build());

  @Override
  public Charge charge(ChargeCommand command) {
    log.info("Charging order {} for {} cents", command.orderId(), command.amountCents());
    Charge charge = activities.capturePayment(command);
    log.info("Charge {} {} for order {}", charge.chargeId(), charge.status(), command.orderId());
    return charge;
  }
}
