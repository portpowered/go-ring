"""Replay all sanitized HTTP exchanges through Python's public raw query API."""

import time

import pytest
from ring_doorbell import Auth

from shared_fixture_harness import http_cases, load_case, query, replay_http, request_url


@pytest.mark.asyncio
@pytest.mark.parametrize("path", http_cases(), ids=lambda path: str(path.parent.name + "/" + path.name))
async def test_python_raw_query_replays_shared_http_case(path):
    case = load_case(path)
    request, response = case["request"], case["response"]
    token = {
        "access_token": "synthetic-test-token",
        "token_type": "Bearer",
        "expires_in": 3600,
        "expires_at": time.time() + 3600,
        "scope": "client",
    }
    auth = Auth("go-ring-fixture-harness", token, hardware_id="synthetic-hardware")
    try:
        async with replay_http(case):
            result = await auth.async_query(
                request_url(case),
                method=request["method"],
                extra_params=query(case),
                json=request["body"] if request["json"] else None,
            )
        assert result.status_code == response["status"]
        if response["json"]:
            assert result.json() == response["body"]
        else:
            assert result.content == b""
    finally:
        await auth.async_close()
