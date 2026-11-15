// Package workflows contains only deterministic workflow code. No I/O, no
// time.Now, no goroutines, no map iteration — see workflowcheck in the Makefile.
package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/activities"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/shared"
)

// OrderWorkflow is the order saga: reserve inventory -> charge payment -> ship -> notify.
// (Slice 1: happy path. Compensation lands in phase 2.)
func OrderWorkflow(ctx workflow.Context, in shared.OrderInput) (shared.OrderResult, error) {
	logger := workflow.GetLogger(ctx)
	result := shared.OrderResult{OrderID: in.OrderID}

	// Remote activities: real HTTP calls to other services. Retries are where
	// Temporal earns its keep — a 5xx or connection reset is retried with
	// backoff; a 4xx comes back as a non-retryable ApplicationError.
	remoteCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
		},
	})

	// Local activities: fast writes to this service's own SQLite DB. They run
	// in the worker process without a round trip through the task queue.
	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
	})

	var a *activities.Activities // nil receiver: only used to reference method names

	record := func(u shared.OrderStatusUpdate) error {
		u.OrderID = in.OrderID
		result.Status = u.Status
		return workflow.ExecuteLocalActivity(localCtx, a.RecordOrderStatus, u).Get(ctx, nil)
	}
	fail := func(step string, err error) (shared.OrderResult, error) {
		logger.Error("Order failed", "step", step, "error", err)
		reason := fmt.Sprintf("%s: %v", step, err)
		_ = record(shared.OrderStatusUpdate{Status: shared.StatusFailed, FailureReason: reason})
		return result, err
	}

	if err := record(shared.OrderStatusUpdate{Order: &in, Status: shared.StatusReceived}); err != nil {
		return result, err
	}

	// 1. Reserve inventory at batcave-armory.
	if err := record(shared.OrderStatusUpdate{Status: shared.StatusReserving}); err != nil {
		return result, err
	}
	var reservation shared.Reservation
	err := workflow.ExecuteActivity(remoteCtx, a.ReserveInventory,
		shared.ReserveRequest{OrderID: in.OrderID, Items: in.Items}).Get(ctx, &reservation)
	if err != nil {
		return fail("reserve inventory", err)
	}
	result.ReservationID = reservation.ReservationID
	result.TotalCents = reservation.TotalCents

	// 2. Charge payment at wayne-billing.
	if err := record(shared.OrderStatusUpdate{Status: shared.StatusCharging,
		ReservationID: reservation.ReservationID, TotalCents: reservation.TotalCents}); err != nil {
		return result, err
	}
	var charge shared.Charge
	err = workflow.ExecuteActivity(remoteCtx, a.ChargePayment, shared.ChargeRequest{
		OrderID:     in.OrderID,
		CustomerID:  in.CustomerID,
		AmountCents: reservation.TotalCents,
		Currency:    "USD",
	}).Get(ctx, &charge)
	if err != nil {
		return fail("charge payment", err)
	}
	result.ChargeID = charge.ChargeID

	// 3. Ship from batcave-armory.
	if err := record(shared.OrderStatusUpdate{Status: shared.StatusShipping, ChargeID: charge.ChargeID}); err != nil {
		return result, err
	}
	var shipment shared.Shipment
	err = workflow.ExecuteActivity(remoteCtx, a.ShipOrder, shared.ShipRequest{
		OrderID:       in.OrderID,
		ReservationID: reservation.ReservationID,
		Address:       in.ShippingAddress,
	}).Get(ctx, &shipment)
	if err != nil {
		return fail("ship order", err)
	}
	result.TrackingNumber = shipment.TrackingNumber

	// 4. Notify the customer.
	if err := record(shared.OrderStatusUpdate{Status: shared.StatusNotifying, TrackingNumber: shipment.TrackingNumber}); err != nil {
		return result, err
	}
	err = workflow.ExecuteActivity(remoteCtx, a.NotifyCustomer, shared.NotifyRequest{
		OrderID:        in.OrderID,
		CustomerID:     in.CustomerID,
		TrackingNumber: shipment.TrackingNumber,
		TotalCents:     reservation.TotalCents,
	}).Get(ctx, nil)
	if err != nil {
		return fail("notify customer", err)
	}

	if err := record(shared.OrderStatusUpdate{Status: shared.StatusCompleted}); err != nil {
		return result, err
	}
	logger.Info("Order completed", "orderId", in.OrderID, "tracking", shipment.TrackingNumber)
	return result, nil
}
