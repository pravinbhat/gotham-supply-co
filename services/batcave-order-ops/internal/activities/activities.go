// Package activities is where all side effects of the order saga live: HTTP
// calls to wayne-billing and batcave-armory, and writes to the local orders DB.
package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/shared"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/store"
)

type Activities struct {
	HTTP       *http.Client
	Store      *store.Store
	BillingURL string
	ArmoryURL  string
}

// ReserveInventory -> POST batcave-armory /reservations.
// Idempotent on order_id: a retry after a lost response returns the same reservation.
func (a *Activities) ReserveInventory(ctx context.Context, req shared.ReserveRequest) (shared.Reservation, error) {
	var out shared.Reservation
	err := a.postJSON(ctx, a.ArmoryURL+"/reservations", "", req, &out)
	return out, err
}

// ChargePayment -> POST wayne-billing /charges with an Idempotency-Key.
// The key is derived from the order, NOT from the activity attempt, so every
// retry of this activity maps to the same charge on the billing side.
func (a *Activities) ChargePayment(ctx context.Context, req shared.ChargeRequest) (shared.Charge, error) {
	var out shared.Charge
	err := a.postJSON(ctx, a.BillingURL+"/charges", "order-"+req.OrderID, req, &out)
	return out, err
}

// ShipOrder -> POST batcave-armory /shipments (idempotent on order_id).
func (a *Activities) ShipOrder(ctx context.Context, req shared.ShipRequest) (shared.Shipment, error) {
	var out shared.Shipment
	err := a.postJSON(ctx, a.ArmoryURL+"/shipments", "", req, &out)
	return out, err
}

// NotifyCustomer records a customer notification. For now it's a local outbox
// row + log line; once alfred exists this becomes a call to it.
func (a *Activities) NotifyCustomer(ctx context.Context, req shared.NotifyRequest) error {
	msg := fmt.Sprintf("Master %s, your order %s is on its way (tracking %s). Total: $%.2f. — Alfred",
		req.CustomerID, req.OrderID, req.TrackingNumber, float64(req.TotalCents)/100)
	if err := a.Store.InsertNotification(ctx, req.OrderID, req.CustomerID, msg); err != nil {
		return err
	}
	activity.GetLogger(ctx).Info("Customer notified", "orderId", req.OrderID, "message", msg)
	return nil
}

// RecordOrderStatus is run as a LOCAL activity — it only touches this
// service's own DB, so there's no need for a task-queue round trip.
func (a *Activities) RecordOrderStatus(ctx context.Context, u shared.OrderStatusUpdate) error {
	return a.Store.ApplyStatusUpdate(ctx, u)
}

// ---- HTTP plumbing ----

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// postJSON encodes the cross-service error contract:
//   - network error / 5xx / 408 / 429  -> plain error, Temporal retries it
//   - any other 4xx                     -> non-retryable ApplicationError whose
//     Type is the remote error code (e.g. OUT_OF_STOCK), so the workflow can
//     branch on it with errors.As + ApplicationError.Type().
func (a *Activities) postJSON(ctx context.Context, url, idemKey string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("encode request", "ENCODE_ERROR", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return temporal.NewNonRetryableApplicationError("build request", "BAD_REQUEST", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err) // retryable
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return temporal.NewNonRetryableApplicationError("decode response", "DECODE_ERROR", err)
		}
		return nil
	}

	var ae apiError
	_ = json.Unmarshal(raw, &ae)
	if ae.Error == "" {
		ae.Error = fmt.Sprintf("HTTP_%d", resp.StatusCode)
	}
	msg := fmt.Sprintf("POST %s -> %d %s: %s", url, resp.StatusCode, ae.Error, ae.Message)

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
		return temporal.NewApplicationError(msg, ae.Error) // retryable
	}
	return temporal.NewNonRetryableApplicationError(msg, ae.Error, errors.New(ae.Message))
}
