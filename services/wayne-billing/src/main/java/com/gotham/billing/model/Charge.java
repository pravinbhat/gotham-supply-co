package com.gotham.billing.model;

/** A charge as stored in the billing DB and returned over HTTP. */
public record Charge(
    String chargeId,
    String idempotencyKey,
    String orderId,
    String customerId,
    String status,
    long amountCents,
    String currency,
    String merchant,
    String createdAt) {

  public static final String MERCHANT = "Wayne Enterprises (fan-made demo)";
}
