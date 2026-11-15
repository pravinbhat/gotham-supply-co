// Package shared holds types that cross the workflow/activity boundary and the
// HTTP contracts with the other Gotham services. Everything here is serialized
// into Temporal event history, so treat field changes like a wire-format change.
package shared

const TaskQueue = "batcave-order-ops"

// WorkflowIDForOrder is the single place the order -> workflow ID mapping lives.
// Using a business ID (not a random UUID) is what makes "start order" idempotent.
func WorkflowIDForOrder(orderID string) string { return "order-" + orderID }

type LineItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type Address struct {
	Name    string `json:"name"`
	Street  string `json:"street"`
	City    string `json:"city"`
	Country string `json:"country"`
}

type OrderInput struct {
	OrderID         string     `json:"order_id"`
	CustomerID      string     `json:"customer_id"`
	Items           []LineItem `json:"items"`
	ShippingAddress Address    `json:"shipping_address"`
}

type OrderStatus string

const (
	StatusReceived  OrderStatus = "RECEIVED"
	StatusReserving OrderStatus = "RESERVING_INVENTORY"
	StatusCharging  OrderStatus = "CHARGING_PAYMENT"
	StatusShipping  OrderStatus = "SHIPPING"
	StatusNotifying OrderStatus = "NOTIFYING_CUSTOMER"
	StatusCompleted OrderStatus = "COMPLETED"
	StatusFailed    OrderStatus = "FAILED"
)

type OrderResult struct {
	OrderID        string      `json:"order_id"`
	Status         OrderStatus `json:"status"`
	ReservationID  string      `json:"reservation_id"`
	ChargeID       string      `json:"charge_id"`
	TrackingNumber string      `json:"tracking_number"`
	TotalCents     int64       `json:"total_cents"`
}

// OrderStatusUpdate is what the workflow asks the local DB to record. Empty
// fields mean "leave unchanged".
type OrderStatusUpdate struct {
	Order          *OrderInput `json:"order,omitempty"` // only on the first update (upsert)
	OrderID        string      `json:"order_id"`
	Status         OrderStatus `json:"status"`
	ReservationID  string      `json:"reservation_id,omitempty"`
	ChargeID       string      `json:"charge_id,omitempty"`
	TrackingNumber string      `json:"tracking_number,omitempty"`
	TotalCents     int64       `json:"total_cents,omitempty"`
	FailureReason  string      `json:"failure_reason,omitempty"`
}

// ---- batcave-armory contracts ----

type ReserveRequest struct {
	OrderID string     `json:"order_id"`
	Items   []LineItem `json:"items"`
}

type ReservedLine struct {
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	WarehouseID    string `json:"warehouse_id"`
}

type Reservation struct {
	ReservationID string         `json:"reservation_id"`
	OrderID       string         `json:"order_id"`
	Status        string         `json:"status"`
	Lines         []ReservedLine `json:"lines"`
	TotalCents    int64          `json:"total_cents"`
}

type ShipRequest struct {
	OrderID       string  `json:"order_id"`
	ReservationID string  `json:"reservation_id"`
	Address       Address `json:"address"`
}

type Shipment struct {
	ShipmentID     string `json:"shipment_id"`
	OrderID        string `json:"order_id"`
	Carrier        string `json:"carrier"`
	TrackingNumber string `json:"tracking_number"`
	Status         string `json:"status"`
}

// ---- wayne-billing contracts ----

type ChargeRequest struct {
	OrderID     string `json:"order_id"`
	CustomerID  string `json:"customer_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

type Charge struct {
	ChargeID    string `json:"charge_id"`
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Merchant    string `json:"merchant"`
}

type NotifyRequest struct {
	OrderID        string `json:"order_id"`
	CustomerID     string `json:"customer_id"`
	TrackingNumber string `json:"tracking_number"`
	TotalCents     int64  `json:"total_cents"`
}
