"""batcave-armory's own SQLite database: catalog, per-warehouse stock, reservations, shipments.

Stock model: on_hand is physical units in a warehouse; reserved is units promised to
open reservations. available = on_hand - reserved. Reserve bumps `reserved`; pick
(fulfillment) decrements both; release (phase 2) decrements `reserved` only.
"""

import json
import secrets
import sqlite3
import uuid
from contextlib import contextmanager
from datetime import UTC, datetime
from pathlib import Path

from .errors import ArmoryError
from .models import Address, LineItem, PickResult, Reservation, ReservedLine, Shipment

SCHEMA = """
CREATE TABLE IF NOT EXISTS products (
  sku          TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL,
  price_cents  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS warehouses (
  warehouse_id TEXT PRIMARY KEY,
  name         TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS stock (
  sku          TEXT NOT NULL REFERENCES products(sku),
  warehouse_id TEXT NOT NULL REFERENCES warehouses(warehouse_id),
  on_hand      INTEGER NOT NULL CHECK (on_hand >= 0),
  reserved     INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0 AND reserved <= on_hand),
  PRIMARY KEY (sku, warehouse_id)
);
CREATE TABLE IF NOT EXISTS reservations (
  reservation_id TEXT PRIMARY KEY,
  order_id       TEXT NOT NULL UNIQUE,     -- idempotency: one reservation per order
  status         TEXT NOT NULL,            -- HELD | PICKED | RELEASED
  total_cents    INTEGER NOT NULL,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reservation_lines (
  reservation_id   TEXT NOT NULL REFERENCES reservations(reservation_id),
  sku              TEXT NOT NULL,
  warehouse_id     TEXT NOT NULL,
  quantity         INTEGER NOT NULL,
  unit_price_cents INTEGER NOT NULL,
  PRIMARY KEY (reservation_id, sku)
);
CREATE TABLE IF NOT EXISTS shipments (
  shipment_id     TEXT PRIMARY KEY,
  order_id        TEXT NOT NULL UNIQUE,    -- idempotency: one shipment per order
  reservation_id  TEXT NOT NULL,
  carrier         TEXT NOT NULL,
  tracking_number TEXT NOT NULL,
  status          TEXT NOT NULL,
  address_json    TEXT NOT NULL,
  created_at      TEXT NOT NULL
);
"""

PRODUCTS = [
    ("BAT-001", "Batarang Replica", "Die-cast metal display batarang with stand.", 2499),
    ("BAT-002", "Utility Belt Prop Replica", "Full-size belt with seven working pouches.", 8999),
    ("BAT-003", "Cowl Display Bust", "1:2 scale resin bust. Limited run.", 14999),
    ("BAT-004", "Bat-Signal Desk Lamp", "USB lamp that projects the signal on your ceiling.", 4999),
    ("BAT-005", "1:18 Batmobile Die-cast", "Detailed die-cast with opening canopy.", 7999),
    ("BAT-006", "Grapnel Launcher Prop", "Non-functional display prop. Please do not scale buildings.", 5999),
    ("GTH-101", "Gotham Skyline Art Print", "18x24 print of the city at dusk.", 2999),
    ("GTH-102", "Wayne Manor Puzzle", "1000-piece jigsaw of the manor grounds.", 3499),
    ("RGS-201", "Rogues Gallery Playing Cards", "Poker deck featuring Gotham's most wanted.", 1499),
    ("ALF-301", "Alfred's Tea Service Mug", "Ceramic mug and loose-leaf tin. Earl Grey, naturally.", 2799),
]

WAREHOUSES = [
    ("GCD-01", "Gotham Central Depot"),
    ("WTV-02", "Wayne Tower Vault"),
    ("BCV-03", "Batcave Sub-Level 2"),
]

# (sku, warehouse, on_hand). BAT-003 is deliberately scarce for out-of-stock testing.
STOCK = [
    ("BAT-001", "GCD-01", 40), ("BAT-001", "BCV-03", 25),
    ("BAT-002", "WTV-02", 12), ("BAT-002", "BCV-03", 8),
    ("BAT-003", "WTV-02", 2),
    ("BAT-004", "GCD-01", 30), ("BAT-004", "WTV-02", 10),
    ("BAT-005", "GCD-01", 15), ("BAT-005", "BCV-03", 15),
    ("BAT-006", "BCV-03", 6),
    ("GTH-101", "GCD-01", 50),
    ("GTH-102", "GCD-01", 20), ("GTH-102", "WTV-02", 20),
    ("RGS-201", "GCD-01", 100),
    ("ALF-301", "WTV-02", 35),
]


def _now() -> str:
    return datetime.now(UTC).isoformat()


class Store:
    def __init__(self, path: str):
        self.path = path
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        with self._conn() as c:
            c.execute("PRAGMA journal_mode=WAL")
            c.executescript(SCHEMA)
            if c.execute("SELECT COUNT(*) FROM products").fetchone()[0] == 0:
                c.executemany("INSERT INTO products VALUES (?,?,?,?)", PRODUCTS)
                c.executemany("INSERT INTO warehouses VALUES (?,?)", WAREHOUSES)
                c.executemany(
                    "INSERT INTO stock (sku, warehouse_id, on_hand) VALUES (?,?,?)", STOCK
                )

    @contextmanager
    def _conn(self):
        c = sqlite3.connect(self.path, timeout=5, isolation_level=None)
        c.row_factory = sqlite3.Row
        try:
            yield c
        finally:
            c.close()

    @contextmanager
    def _tx(self):
        """Write transaction. BEGIN IMMEDIATE takes the write lock up front so the
        read-check-write in reserve() can't race another reservation."""
        with self._conn() as c:
            c.execute("BEGIN IMMEDIATE")
            try:
                yield c
                c.execute("COMMIT")
            except BaseException:
                c.execute("ROLLBACK")
                raise

    # ---- catalog / inventory ----

    def list_products(self) -> list[dict]:
        with self._conn() as c:
            return [dict(r) for r in c.execute("SELECT * FROM products ORDER BY sku")]

    def inventory(self) -> list[dict]:
        with self._conn() as c:
            rows = c.execute(
                """SELECT s.sku, p.name, s.warehouse_id, s.on_hand, s.reserved,
                          s.on_hand - s.reserved AS available
                   FROM stock s JOIN products p USING (sku)
                   ORDER BY s.sku, s.warehouse_id"""
            )
            return [dict(r) for r in rows]

    # ---- reservations ----

    def reserve(self, order_id: str, items: list[LineItem]) -> Reservation:
        """Atomically hold stock for every line, or nothing. Idempotent on order_id."""
        if not items:
            raise ArmoryError(400, "BAD_REQUEST", "at least one item is required")
        with self._tx() as c:
            existing = c.execute(
                "SELECT reservation_id FROM reservations WHERE order_id = ?", (order_id,)
            ).fetchone()
            if existing:
                return self._load_reservation(c, existing["reservation_id"])

            lines: list[ReservedLine] = []
            for item in items:
                if item.quantity <= 0:
                    raise ArmoryError(400, "BAD_REQUEST", f"quantity must be positive for {item.sku}")
                product = c.execute("SELECT * FROM products WHERE sku = ?", (item.sku,)).fetchone()
                if product is None:
                    raise ArmoryError(422, "UNKNOWN_SKU", f"no such product {item.sku}")
                # First-fit: the warehouse with the most availability that can cover the line.
                wh = c.execute(
                    """SELECT warehouse_id FROM stock
                       WHERE sku = ? AND on_hand - reserved >= ?
                       ORDER BY on_hand - reserved DESC LIMIT 1""",
                    (item.sku, item.quantity),
                ).fetchone()
                if wh is None:
                    raise ArmoryError(
                        409, "OUT_OF_STOCK",
                        f"not enough {product['name']} ({item.sku}) in any single warehouse",
                    )
                c.execute(
                    "UPDATE stock SET reserved = reserved + ? WHERE sku = ? AND warehouse_id = ?",
                    (item.quantity, item.sku, wh["warehouse_id"]),
                )
                lines.append(ReservedLine(item.sku, product["name"], item.quantity,
                                          product["price_cents"], wh["warehouse_id"]))

            reservation_id = "rsv_" + uuid.uuid4().hex[:12]
            total = sum(line.quantity * line.unit_price_cents for line in lines)
            ts = _now()
            c.execute(
                "INSERT INTO reservations VALUES (?,?,?,?,?,?)",
                (reservation_id, order_id, "HELD", total, ts, ts),
            )
            c.executemany(
                "INSERT INTO reservation_lines VALUES (?,?,?,?,?)",
                [(reservation_id, line.sku, line.warehouse_id, line.quantity, line.unit_price_cents)
                 for line in lines],
            )
            return Reservation(reservation_id, order_id, "HELD", lines, total)

    def get_reservation(self, reservation_id: str) -> Reservation:
        with self._conn() as c:
            return self._load_reservation(c, reservation_id)

    def _load_reservation(self, c: sqlite3.Connection, reservation_id: str) -> Reservation:
        r = c.execute("SELECT * FROM reservations WHERE reservation_id = ?", (reservation_id,)).fetchone()
        if r is None:
            raise ArmoryError(404, "RESERVATION_NOT_FOUND", f"no reservation {reservation_id}")
        lines = [
            ReservedLine(row["sku"], row["name"], row["quantity"], row["unit_price_cents"], row["warehouse_id"])
            for row in c.execute(
                """SELECT l.*, p.name FROM reservation_lines l JOIN products p USING (sku)
                   WHERE reservation_id = ? ORDER BY l.sku""",
                (reservation_id,),
            )
        ]
        return Reservation(r["reservation_id"], r["order_id"], r["status"], lines, r["total_cents"])

    # ---- fulfillment ----

    def pick(self, reservation_id: str) -> PickResult:
        """Convert a HELD reservation into physically picked stock. Idempotent."""
        with self._tx() as c:
            res = self._load_reservation(c, reservation_id)
            warehouses = sorted({line.warehouse_id for line in res.lines})
            units = sum(line.quantity for line in res.lines)
            if res.status == "PICKED":
                return PickResult(reservation_id, units, warehouses)
            if res.status != "HELD":
                raise ArmoryError(409, "RESERVATION_NOT_HELD", f"reservation is {res.status}")
            for line in res.lines:
                c.execute(
                    """UPDATE stock SET on_hand = on_hand - ?, reserved = reserved - ?
                       WHERE sku = ? AND warehouse_id = ?""",
                    (line.quantity, line.quantity, line.sku, line.warehouse_id),
                )
            c.execute(
                "UPDATE reservations SET status = 'PICKED', updated_at = ? WHERE reservation_id = ?",
                (_now(), reservation_id),
            )
            return PickResult(reservation_id, units, warehouses)

    def create_shipment(self, order_id: str, reservation_id: str, address: Address) -> Shipment:
        """Generate a label and record the shipment. Idempotent on order_id."""
        with self._tx() as c:
            row = c.execute("SELECT * FROM shipments WHERE order_id = ?", (order_id,)).fetchone()
            if row is None:
                shipment_id = "shp_" + uuid.uuid4().hex[:12]
                tracking = "GCP" + "".join(secrets.choice("0123456789") for _ in range(10))
                c.execute(
                    "INSERT INTO shipments VALUES (?,?,?,?,?,?,?,?)",
                    (shipment_id, order_id, reservation_id, "Gotham City Parcel", tracking,
                     "SHIPPED", json.dumps(address.__dict__), _now()),
                )
                row = c.execute("SELECT * FROM shipments WHERE order_id = ?", (order_id,)).fetchone()
            return _shipment(row)

    def get_shipment(self, order_id: str) -> Shipment | None:
        with self._conn() as c:
            row = c.execute("SELECT * FROM shipments WHERE order_id = ?", (order_id,)).fetchone()
            return _shipment(row) if row else None


def _shipment(row: sqlite3.Row) -> Shipment:
    return Shipment(row["shipment_id"], row["order_id"], row["carrier"],
                    row["tracking_number"], row["status"])
