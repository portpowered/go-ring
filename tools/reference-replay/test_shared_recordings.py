"""Adapt sanitized C1 device recordings into the Python reference model."""

import json
import socket
import time
from pathlib import Path

import pytest
from aioresponses import CallbackResult, aioresponses
from ring_doorbell import Auth
from ring_doorbell.const import (
    DEVICES_ENDPOINT,
    INDOOR_CAM_PTZ_KINDS,
    URL_DOORBELL_HISTORY,
    SETTINGS_ENDPOINT,
)
from ring_doorbell.ring import Ring


ROOT = Path(__file__).resolve().parents[2]
DEVICE_LIST = ROOT / "tests" / "replay" / "fixtures" / "recordings" / "http" / "device-list.json"
HTTP_RECORDINGS = ROOT / "tests" / "replay" / "fixtures" / "recordings" / "http"


def load_recorded_devices() -> tuple[dict, list[dict]]:
    exchange = json.loads(DEVICE_LIST.read_text(encoding="utf-8"))
    response = exchange["response"]["body"]
    return exchange, response["devices"]


def load_exchange(name: str) -> dict:
    return json.loads((HTTP_RECORDINGS / name).read_text(encoding="utf-8"))


def adapt_device_families(devices: list[dict]) -> dict[str, dict[int, dict]]:
    """Map the currently captured PTZ device to Python's family index shape."""
    families: dict[str, dict[int, dict]] = {"stickup_cams": {}}
    for device in devices:
        kind = device["kind"]
        if kind in INDOOR_CAM_PTZ_KINDS:
            families["stickup_cams"][device["id"]] = device
        else:
            raise AssertionError(f"no Python family adapter for captured kind {kind!r}")
    return families


def test_python_stickup_model_reads_c1_recording_attributes() -> None:
    """Pair C1 values with Python test_ring.py's basic/stickup attributes."""
    _, devices = load_recorded_devices()
    ring = Ring(auth=None)  # Accessing cached model values does not perform I/O.
    ring.devices_data = adapt_device_families(devices)

    cams = ring.devices().stickup_cams
    assert len(cams) == 1
    cam = cams[0]
    recorded = devices[0]
    assert cam.id == recorded["id"]
    assert cam.name == recorded["description"]
    assert cam.kind == recorded["kind"]
    assert cam.device_id == recorded["device_id"]
    assert cam.model == "Pan-Tilt Indoor Cam"
    assert cam.address == recorded["address"]
    assert cam.timezone == recorded["time_zone"]
    assert cam.firmware == recorded["firmware_version"]


def test_c1_device_list_route_is_distinct_from_python_legacy_route() -> None:
    """Keep the supported-route difference explicit instead of marking it pass."""
    exchange, _ = load_recorded_devices()
    captured_path = exchange["request"]["path"]
    assert captured_path == "/device_info/v3/devices"
    assert DEVICES_ENDPOINT == "/clients_api/ring_devices"
    assert captured_path != DEVICES_ENDPOINT


def test_offline_guard_rejects_remote_resolution_and_connection() -> None:
    with pytest.raises(OSError, match="blocked name resolution"):
        socket.getaddrinfo("example.com", 443)
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock, pytest.raises(
        OSError, match="blocked connection"
    ):
        sock.connect(("198.51.100.1", 443))
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock, pytest.raises(
        OSError, match="blocked datagram"
    ):
        sock.sendto(b"blocked", ("198.51.100.1", 53))


def make_python_device() -> tuple[Auth, Ring, object, dict]:
    _, devices = load_recorded_devices()
    settings_exchange = load_exchange("device-settings-get.json")
    device = dict(devices[0])
    # Python's model reads motion_detection_enabled from the legacy cached
    # `settings` object. Adapt this one value from the captured v3 settings DTO.
    device["settings"] = {
        "motion_detection_enabled": settings_exchange["response"]["body"][
            "motion_settings"
        ]["motion_detection_enabled"]
    }
    ring = Ring(auth=None)
    ring.devices_data = {"stickup_cams": {device["id"]: device}}
    # Avoid the unrelated session-registration request. This adapter only
    # exercises the recorded feature request and has no session cassette.
    ring.session = {"recorded": True}
    cam = ring.devices().stickup_cams[0]
    token = {
        "access_token": "recorded-test-token",
        "token_type": "Bearer",
        "expires_in": 3600,
        "expires_at": time.time() + 3600,
        "scope": "client",
    }
    auth = Auth("go-ring-reference-adapter", token, hardware_id="recorded-device")
    ring.auth = auth
    return auth, ring, cam, device


async def replay_python_request(
    exchange: dict, device: dict, call
) -> None:
    """Replay via aioresponses, matching method, route, query, and JSON body.

    This is transport replay for the recorded request contract; it deliberately
    does not claim exact request-header parity.
    """
    request = exchange["request"]
    response = exchange["response"]
    path = request["path"].replace("{device_id}", str(device["id"]))
    url = request["origin"] + path
    response_headers = {
        key: value[0] if isinstance(value, list) else value
        for key, value in response.get("headers", {}).items()
    }

    def callback(actual_url, **kwargs):
        assert actual_url.path == path
        recorded_query = {pair["name"]: pair["value"] for pair in request["query"]}
        assert {key: str(value) for key, value in (kwargs.get("params") or {}).items()} == recorded_query
        if request["json"]:
            assert kwargs.get("json") == request["body"]
        else:
            assert kwargs.get("data") is None

        recorded_body = response["body"]
        if response.get("json", False):
            return CallbackResult(
                payload=recorded_body,
                status=response["status"],
                headers=response_headers,
            )
        return CallbackResult(
            body=b"" if recorded_body is None else recorded_body,
            status=response["status"],
            headers=response_headers,
        )

    with aioresponses() as mocked:
        mocked.add(url, method=request["method"], callback=callback)
        await call()
        mocked.assert_called_once()


@pytest.mark.asyncio
async def test_python_motion_toggle_matches_captured_settings_patch() -> None:
    """Replay Python's motion control through the matching PATCH cassette."""
    auth, _ring, cam, device = make_python_device()
    exchange = load_exchange("device-settings-patch.json")
    try:
        await replay_python_request(
            exchange,
            device,
            lambda: cam.async_set_motion_detection(True),
        )
    finally:
        await auth.async_close()


@pytest.mark.asyncio
async def test_python_siren_off_matches_captured_control() -> None:
    """Replay the Python off command; this capture has no query or body."""
    auth, _ring, cam, device = make_python_device()
    exchange = load_exchange("siren-off.json")
    try:
        await replay_python_request(exchange, device, lambda: cam.async_set_siren(0))
    finally:
        await auth.async_close()


@pytest.mark.asyncio
async def test_python_siren_on_duration_is_not_present_in_c1_request() -> None:
    """Record the Python command shape and assert the captured query gap."""
    _auth, _ring, cam, device = make_python_device()
    exchange = load_exchange("siren-on.json")
    calls = []

    async def capture_query(path, **kwargs):
        calls.append((path, kwargs))

    cam._ring.async_query = capture_query
    await cam.async_set_siren(30)
    request = exchange["request"]
    captured_path = request["path"].replace("{device_id}", str(device["id"]))
    assert calls == [(captured_path, {"extra_params": {"duration": 30}, "method": "PUT"})]
    assert request["query"] == []


def test_python_history_and_c1_feed_routes_remain_distinct() -> None:
    """The Python legacy history response cannot be asserted from EVM feeds."""
    timeline = load_exchange("device-timeline.json")
    history = load_exchange("history-devices.json")
    assert URL_DOORBELL_HISTORY == "/clients_api/doorbots/{0}/history"
    assert timeline["request"]["path"] == "/evm/v2/timeline/devices/{device_id}"
    assert history["request"]["path"] == "/evm/v3/history/devices"
    assert isinstance(timeline["response"]["body"].get("items"), list)
    assert history["response"]["body"].get("schema") == "GroupedFeedResponse"


def test_python_settings_path_matches_c1_but_inventory_route_does_not() -> None:
    """Route comparison only: this does not equate legacy inventory payloads."""
    settings = load_exchange("device-settings-get.json")
    assert SETTINGS_ENDPOINT.format("{device_id}") == settings["request"]["path"]
    inventory, _ = load_recorded_devices()
    assert inventory["request"]["path"] != DEVICES_ENDPOINT
