"""Activities: every side effect of fulfillment lives here (sync, run in a thread pool)."""

from temporalio import activity
from temporalio.exceptions import ApplicationError

from .db import Store
from .errors import ArmoryError
from .models import PickResult, Shipment, ShipRequest


class ArmoryActivities:
    def __init__(self, store: Store):
        self.store = store

    @activity.defn
    def pick_and_pack(self, reservation_id: str) -> PickResult:
        try:
            result = self.store.pick(reservation_id)
        except ArmoryError as e:
            # Business errors won't fix themselves on retry.
            raise ApplicationError(e.message, type=e.code, non_retryable=True) from e
        activity.logger.info(
            "Picked %d units for %s from %s", result.units_picked, reservation_id, result.warehouses
        )
        return result

    @activity.defn
    def create_shipping_label(self, req: ShipRequest) -> Shipment:
        shipment = self.store.create_shipment(req.order_id, req.reservation_id, req.address)
        activity.logger.info("Label %s created for order %s", shipment.tracking_number, req.order_id)
        return shipment
