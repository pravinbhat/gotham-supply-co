package com.gotham.billing.activities;

import com.gotham.billing.db.ChargeStore;
import com.gotham.billing.model.Charge;
import com.gotham.billing.model.ChargeCommand;
import io.temporal.activity.Activity;
import org.slf4j.Logger;

public class BillingActivitiesImpl implements BillingActivities {
  private static final Logger log = org.slf4j.LoggerFactory.getLogger(BillingActivitiesImpl.class);
  private final ChargeStore store;

  public BillingActivitiesImpl(ChargeStore store) {
    this.store = store;
  }

  @Override
  public Charge capturePayment(ChargeCommand command) {
    int attempt = Activity.getExecutionContext().getInfo().getAttempt();
    // Insert-or-return-existing keyed on the idempotency key: a retried attempt (or a
    // duplicate workflow) can never create a second charge.
    Charge charge = store.insertIfAbsent(command, "CAPTURED");
    log.info("capturePayment order={} attempt={} -> {}", command.orderId(), attempt, charge.chargeId());
    return charge;
  }
}
