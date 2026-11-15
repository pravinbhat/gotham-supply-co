"""Payloads that cross the HTTP boundary and/or Temporal event history.

Plain dataclasses: FastAPI validates them as request bodies, and Temporal's default
data converter serializes them. Changing fields here is a wire-format change.
"""

from dataclasses import dataclass, field


@dataclass
class LineItem:
    sku: str
    quantity: int


@dataclass
class Address:
    name: str = ""
    street: str = ""
    city: str = ""
    country: str = ""


@dataclass
class ReserveRequest:
    order_id: str
    items: list[LineItem]


@dataclass
class ReservedLine:
    sku: str
    name: str
    quantity: int
    unit_price_cents: int
    warehouse_id: str


@dataclass
class Reservation:
    reservation_id: str
    order_id: str
    status: str
    lines: list[ReservedLine] = field(default_factory=list)
    total_cents: int = 0


@dataclass
class ShipRequest:
    order_id: str
    reservation_id: str
    address: Address = field(default_factory=Address)


@dataclass
class PickResult:
    reservation_id: str
    units_picked: int
    warehouses: list[str]


@dataclass
class Shipment:
    shipment_id: str
    order_id: str
    carrier: str
    tracking_number: str
    status: str
