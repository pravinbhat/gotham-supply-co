// Package api is the public REST surface of batcave-order-ops.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/shared"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/store"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/workflows"
)

type Server struct {
	Temporal client.Client
	Store    *store.Store
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /orders", s.createOrder)
	mux.HandleFunc("GET /orders", s.listOrders)
	mux.HandleFunc("GET /orders/{id}", s.getOrder)
	return mux
}

type createOrderRequest struct {
	OrderID         string            `json:"order_id,omitempty"` // optional: client-supplied for idempotent submits
	CustomerID      string            `json:"customer_id"`
	Items           []shared.LineItem `json:"items"`
	ShippingAddress shared.Address    `json:"shipping_address"`
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "BAD_REQUEST", "invalid JSON: "+err.Error())
		return
	}
	if req.CustomerID == "" || len(req.Items) == 0 {
		writeErr(w, 400, "BAD_REQUEST", "customer_id and at least one item are required")
		return
	}
	for _, it := range req.Items {
		if it.SKU == "" || it.Quantity <= 0 {
			writeErr(w, 400, "BAD_REQUEST", "each item needs a sku and a positive quantity")
			return
		}
	}
	if req.OrderID == "" {
		req.OrderID = newOrderID()
	}

	in := shared.OrderInput{
		OrderID:         req.OrderID,
		CustomerID:      req.CustomerID,
		Items:           req.Items,
		ShippingAddress: req.ShippingAddress,
	}
	wfID := shared.WorkflowIDForOrder(in.OrderID)
	run, err := s.Temporal.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{
		ID:        wfID,
		TaskQueue: shared.TaskQueue,
		// An order ID can only ever be processed once, even after the first run closes.
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		// Gotcha: without this, the Go SDK silently hands back a handle to the
		// EXISTING run instead of surfacing WorkflowExecutionAlreadyStarted.
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, workflows.OrderWorkflow, in)
	if err != nil {
		var already *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &already) {
			writeErr(w, 409, "ORDER_EXISTS", "order "+in.OrderID+" was already submitted")
			return
		}
		slog.Error("start workflow failed", "error", err)
		writeErr(w, 503, "TEMPORAL_UNAVAILABLE", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"order_id":    in.OrderID,
		"workflow_id": run.GetID(),
		"run_id":      run.GetRunID(),
		"status":      string(shared.StatusReceived),
	})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.GetOrder(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "NOT_FOUND", "no such order")
		return
	} else if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, 200, o)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.Store.ListOrders(r.Context(), 50)
	if err != nil {
		writeErr(w, 500, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, 200, orders)
}

func newOrderID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "GSC-" + strings.ToUpper(hex.EncodeToString(b))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]string{"error": errCode, "message": msg})
}
