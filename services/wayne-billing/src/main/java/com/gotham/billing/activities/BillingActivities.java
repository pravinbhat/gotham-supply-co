package com.gotham.billing.activities;

import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import io.temporal.activity.ActivityInterface;

@ActivityInterface
public interface BillingActivities {
  /** Captures funds. Must be idempotent on {@code command.idempotencyKey()} — it WILL be retried. */
  Charge capturePayment(ChargeCommand command);
}
