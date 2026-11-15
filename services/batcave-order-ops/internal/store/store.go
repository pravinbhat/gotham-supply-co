// Package store is batcave-order-ops' own SQLite database (orders + notification outbox).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/shared"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type Order struct {
	OrderID         string             `json:"order_id"`
	CustomerID      string             `json:"customer_id"`
	Items           []shared.LineItem  `json:"items"`
	ShippingAddress shared.Address     `json:"shipping_address"`
	Status          shared.OrderStatus `json:"status"`
	TotalCents      int64              `json:"total_cents"`
	ReservationID   string             `json:"reservation_id,omitempty"`
	ChargeID        string             `json:"charge_id,omitempty"`
	TrackingNumber  string             `json:"tracking_number,omitempty"`
	FailureReason   string             `json:"failure_reason,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS orders (
  order_id         TEXT PRIMARY KEY,
  customer_id      TEXT NOT NULL,
  items_json       TEXT NOT NULL,
  address_json     TEXT NOT NULL,
  status           TEXT NOT NULL,
  total_cents      INTEGER NOT NULL DEFAULT 0,
  reservation_id   TEXT NOT NULL DEFAULT '',
  charge_id        TEXT NOT NULL DEFAULT '',
  tracking_number  TEXT NOT NULL DEFAULT '',
  failure_reason   TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notifications (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id    TEXT NOT NULL UNIQUE,  -- one shipped-notification per order: retries are no-ops
  customer_id TEXT NOT NULL,
  message     TEXT NOT NULL,
  created_at  TEXT NOT NULL
);`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writers, avoid SQLITE_BUSY
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ApplyStatusUpdate upserts the order row (first update carries the full order)
// and applies non-empty fields. Safe to re-run: it's last-writer-wins on
// fields the workflow owns.
func (s *Store) ApplyStatusUpdate(ctx context.Context, u shared.OrderStatusUpdate) error {
	ts := now()
	if u.Order != nil {
		items, _ := json.Marshal(u.Order.Items)
		addr, _ := json.Marshal(u.Order.ShippingAddress)
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO orders (order_id, customer_id, items_json, address_json, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(order_id) DO NOTHING`,
			u.OrderID, u.Order.CustomerID, string(items), string(addr), u.Status, ts, ts)
		if err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE orders SET
		  status          = ?,
		  reservation_id  = COALESCE(NULLIF(?, ''), reservation_id),
		  charge_id       = COALESCE(NULLIF(?, ''), charge_id),
		  tracking_number = COALESCE(NULLIF(?, ''), tracking_number),
		  total_cents     = CASE WHEN ? > 0 THEN ? ELSE total_cents END,
		  failure_reason  = COALESCE(NULLIF(?, ''), failure_reason),
		  updated_at      = ?
		WHERE order_id = ?`,
		u.Status, u.ReservationID, u.ChargeID, u.TrackingNumber, u.TotalCents, u.TotalCents,
		u.FailureReason, ts, u.OrderID)
	return err
}

func (s *Store) InsertNotification(ctx context.Context, orderID, customerID, msg string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications (order_id, customer_id, message, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(order_id) DO NOTHING`, orderID, customerID, msg, now())
	return err
}

const orderCols = `order_id, customer_id, items_json, address_json, status, total_cents,
	reservation_id, charge_id, tracking_number, failure_reason, created_at, updated_at`

func scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var items, addr, created, updated string
	err := row.Scan(&o.OrderID, &o.CustomerID, &items, &addr, &o.Status, &o.TotalCents,
		&o.ReservationID, &o.ChargeID, &o.TrackingNumber, &o.FailureReason, &created, &updated)
	if err != nil {
		return o, err
	}
	_ = json.Unmarshal([]byte(items), &o.Items)
	_ = json.Unmarshal([]byte(addr), &o.ShippingAddress)
	o.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	o.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return o, nil
}

func (s *Store) GetOrder(ctx context.Context, id string) (Order, error) {
	o, err := scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM orders WHERE order_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func (s *Store) ListOrders(ctx context.Context, limit int) ([]Order, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+orderCols+` FROM orders ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
