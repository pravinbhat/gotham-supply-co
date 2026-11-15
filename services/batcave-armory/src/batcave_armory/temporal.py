from temporalio.client import Client
from temporalio.envconfig import ClientConfig


async def connect() -> Client:
    """envconfig: TEMPORAL_ADDRESS / TEMPORAL_NAMESPACE / TEMPORAL_API_KEY or TEMPORAL_PROFILE."""
    config = ClientConfig.load_client_connect_config()
    config.setdefault("target_host", "localhost:7233")
    return await Client.connect(**config)
