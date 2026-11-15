package com.gotham.billing.model;

/**
 * Input to {@code ChargeWorkflow}. Serialized into event history, so treat field changes as a
 * wire-format change. The idempotency key comes from the HTTP Idempotency-Key header.
 */
public record ChargeCommand(
    String idempotencyKey, String orderId, String customerId, long amountCents, String currency) {}
