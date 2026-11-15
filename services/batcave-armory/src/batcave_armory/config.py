import os
from dataclasses import dataclass

TASK_QUEUE = "batcave-armory"


@dataclass(frozen=True)
class Settings:
    port: int = int(os.getenv("PORT", "8083"))
    db_path: str = os.getenv("DB_PATH", "data/armory.db")


settings = Settings()


def fulfillment_workflow_id(order_id: str) -> str:
    return f"fulfill-{order_id}"
