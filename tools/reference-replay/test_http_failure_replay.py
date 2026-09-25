"""Synthetic HTTP replay outcomes for the Python query contract."""

import asyncio
import time

import pytest
from aiohttp import ClientConnectionError
from aioresponses import aioresponses
from ring_doorbell import Auth
from ring_doorbell.exceptions import RingError, RingTimeout

from shared_fixture_harness import PORTING, load_case


def make_auth():
    return Auth("go-ring-replay", {
        "access_token": "synthetic", "token_type": "Bearer", "expires_in": 3600,
        "expires_at": time.time() + 3600, "scope": "client",
    }, hardware_id="synthetic-hardware")


@pytest.mark.asyncio
@pytest.mark.parametrize("scenario", load_case(PORTING / "http-failures.json"), ids=lambda item: item["case"])
async def test_python_query_classifies_replayed_http_failures(scenario):
    auth = make_auth()
    url = "https://api.ring.com/synthetic/failure"
    try:
        with aioresponses() as mocked:
            outcome = scenario.get("outcome")
            if outcome == "timeout":
                mocked.get(url, exception=asyncio.TimeoutError())
            elif outcome == "client-error":
                mocked.get(url, exception=ClientConnectionError("synthetic connection lost"))
            elif outcome == "runtime-error":
                mocked.get(url, exception=RuntimeError("synthetic unexpected"))
            else:
                mocked.get(url, status=scenario["status"], payload={"error": "synthetic failure"})
            expected = RingTimeout if outcome == "timeout" else RingError
            with pytest.raises(expected):
                await auth.async_query(url)
            mocked.assert_called_once()
    finally:
        await auth.async_close()
