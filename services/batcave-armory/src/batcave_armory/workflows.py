"""Workflow definitions only — this module is re-imported inside the determinism sandbox."""

from datetime import timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy

with workflow.unsafe.imports_passed_through():
    from .activities import ArmoryActivities
    from .models import Shipment, ShipRequest


@workflow.defn
class FulfillmentWorkflow:
    """Pick -> label. Slice 1 is sequential; phase 4 fans out per warehouse."""

    @workflow.run
    async def run(self, req: ShipRequest) -> Shipment:
        retry = RetryPolicy(initial_interval=timedelta(milliseconds=500),
                            maximum_interval=timedelta(seconds=10))
        picked = await workflow.execute_activity_method(
            ArmoryActivities.pick_and_pack,
            req.reservation_id,
            start_to_close_timeout=timedelta(seconds=15),
            retry_policy=retry,
        )
        workflow.logger.info("Picked %d units from %s", picked.units_picked, picked.warehouses)
        return await workflow.execute_activity_method(
            ArmoryActivities.create_shipping_label,
            req,
            start_to_close_timeout=timedelta(seconds=15),
            retry_policy=retry,
        )
