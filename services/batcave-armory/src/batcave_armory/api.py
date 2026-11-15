"""REST API + embedded Temporal worker for batcave-armory."""

import asyncio
import logging
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager
from dataclasses import asdict

from fastapi import FastAPI, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.responses import JSONResponse
from temporalio.client import Client, WorkflowFailureError
from temporalio.common import WorkflowIDConflictPolicy, WorkflowIDReusePolicy
from temporalio.exceptions import ApplicationError, WorkflowAlreadyStartedError
from temporalio.worker import Worker

from .activities import ArmoryActivities
from .config import TASK_QUEUE, fulfillment_workflow_id, settings
from .db import Store
from .errors import ArmoryError
from .models import ReserveRequest, ShipRequest
from .temporal import connect
from .workflows import FulfillmentWorkflow

log = logging.getLogger("batcave_armory")
SHIPMENT_WAIT_SECONDS = 20  # below the caller's 25s HTTP / 30s activity timeout


@asynccontextmanager
async def lifespan(app: FastAPI):
    store = Store(settings.db_path)
    client = await connect()
    log.info("Connected to Temporal namespace=%s", client.namespace)
    executor = ThreadPoolExecutor(max_workers=16, thread_name_prefix="armory-activity")
    acts = ArmoryActivities(store)
    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[FulfillmentWorkflow],
        activities=[
            acts.pick_and_pack,
            acts.create_shipping_label,
        ],
        activity_executor=executor,
    )
    worker_task = asyncio.create_task(worker.run())
    app.state.store, app.state.client = store, client
    log.info("batcave-armory worker polling taskQueue=%s", TASK_QUEUE)
    try:
        yield
    finally:
        await worker.shutdown()
        await worker_task
        executor.shutdown(wait=False)


app = FastAPI(title="batcave-armory", lifespan=lifespan)


@app.exception_handler(ArmoryError)
async def armory_error(_: Request, e: ArmoryError):
    return JSONResponse(status_code=e.status, content={"error": e.code, "message": e.message})


def _store(request: Request) -> Store:
    return request.app.state.store


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}


@app.get("/products")
def products(request: Request):
    return _store(request).list_products()


@app.get("/inventory")
def inventory(request: Request):
    return _store(request).inventory()


# Sync handlers run in FastAPI's threadpool, so blocking sqlite calls don't stall the event loop.
@app.post("/reservations", status_code=201)
def create_reservation(req: ReserveRequest, request: Request):
    return asdict(_store(request).reserve(req.order_id, req.items))


@app.get("/reservations/{reservation_id}")
def get_reservation(reservation_id: str, request: Request):
    return asdict(_store(request).get_reservation(reservation_id))


@app.get("/shipments/{order_id}")
def get_shipment(order_id: str, request: Request):
    s = _store(request).get_shipment(order_id)
    if s is None:
        raise ArmoryError(404, "NOT_FOUND", f"no shipment for order {order_id}")
    return asdict(s)


@app.post("/shipments")
async def create_shipment(req: ShipRequest, request: Request):
    """Synchronous facade over FulfillmentWorkflow, idempotent on order_id."""
    existing = await run_in_threadpool(_store(request).get_shipment, req.order_id)
    if existing:
        return asdict(existing)

    client: Client = request.app.state.client
    wf_id = fulfillment_workflow_id(req.order_id)
    try:
        handle = await client.start_workflow(
            FulfillmentWorkflow.run,
            req,
            id=wf_id,
            task_queue=TASK_QUEUE,
            id_conflict_policy=WorkflowIDConflictPolicy.USE_EXISTING,
            id_reuse_policy=WorkflowIDReusePolicy.ALLOW_DUPLICATE_FAILED_ONLY,
        )
    except WorkflowAlreadyStartedError:
        handle = client.get_workflow_handle(wf_id)

    try:
        shipment = await asyncio.wait_for(handle.result(), SHIPMENT_WAIT_SECONDS)
    except TimeoutError:
        return JSONResponse(status_code=504, content={
            "error": "FULFILLMENT_IN_PROGRESS", "message": "still fulfilling; retry"})
    except WorkflowFailureError as e:
        # WorkflowFailureError -> ActivityError -> ApplicationError (the business error)
        cause = getattr(e.cause, "cause", None) or e.cause
        if isinstance(cause, ApplicationError) and cause.non_retryable:
            return JSONResponse(status_code=409, content={
                "error": cause.type or "FULFILLMENT_FAILED", "message": cause.message})
        return JSONResponse(status_code=502, content={
            "error": "FULFILLMENT_FAILED", "message": str(e.cause)})
    return asdict(shipment)
