package com.gotham.billing.model;

/** HTTP request body for POST /charges (snake_case on the wire). */
public record ChargeRequest(String orderId, String customerId, long amountCents, String currency) {}
