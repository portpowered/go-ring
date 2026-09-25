"""Adapt sanitized C1 device recordings into the Python reference model."""

import json
import socket
from pathlib import Path

import pytest
from ring_doorbell.ring import Ring
from ring_doorbell.const import DEVICES_ENDPOINT, INDOOR_CAM_PTZ_KINDS


ROOT = Path(__file__).resolve().parents[2]
DEVICE_LIST = ROOT / "test" / "recordings" / "http" / "device-list.json"


def load_recorded_devices() -> tuple[dict, list[dict]]:
    exchange = json.loads(DEVICE_LIST.read_text(encoding="utf-8"))
    response = exchange["response"]["body"]
    return exchange, response["devices"]


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
