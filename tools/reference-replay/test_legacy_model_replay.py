"""Run the Python device model against the same legacy fixture used by Go."""

import json
import time
import re

import pytest
from aioresponses import CallbackResult, aioresponses
from ring_doorbell import Auth
from ring_doorbell.const import DEVICES_ENDPOINT
from ring_doorbell.const import INTERCOM_KINDS
from ring_doorbell.const import (
    HEALTH_CHIMES_ENDPOINT, HEALTH_DOORBELL_ENDPOINT, URL_DOORBELL_HISTORY,
    URL_RECORDING, URL_RECORDING_SHARE_PLAY, SNAPSHOT_TIMESTAMP_ENDPOINT,
    SNAPSHOT_ENDPOINT,
    NEW_SESSION_ENDPOINT,
)
from ring_doorbell.exceptions import RingError
from ring_doorbell.ring import Ring

from shared_fixture_harness import ROOT, PORTING, load_case


def legacy_response(name):
    return json.loads((ROOT / "tests" / "replay" / "fixtures" / "legacy" / name).read_text(encoding="utf-8"))


def legacy_ring():
    token = {
        "access_token": "synthetic-test-token", "token_type": "Bearer",
        "expires_in": 3600, "expires_at": time.time() + 3600, "scope": "client",
    }
    auth = Auth("go-ring-fixture-harness", token, hardware_id="synthetic-hardware")
    ring = Ring(auth)
    ring.session = {"synthetic": True}
    ring.devices_data = {
        family: {device["id"]: device for device in items}
        for family, items in legacy_response("ring_devices.json").items()
    }
    return auth, ring


def add_request(mocked, method, url, *, params=None, body=None, payload=None, check_json=False):
    """Match the request boundary and return a synthetic or shared response."""
    expected_params = params or {}

    def callback(actual_url, **kwargs):
        assert actual_url.path == url.split("api.ring.com", 1)[1]
        assert kwargs.get("params") == expected_params
        if check_json:
            assert kwargs.get("json") == body
        return CallbackResult(payload=payload if payload is not None else {}, status=200)

    mocked.add(re.compile("^" + re.escape(url) + r"(?:\?.*)?$"), method=method, callback=callback)


@pytest.mark.asyncio
async def test_python_inventory_replays_gos_legacy_response_fixture():
    auth, ring = legacy_ring()
    fixture = legacy_response("ring_devices.json")
    try:
        with aioresponses() as mocked:
            mocked.get("https://api.ring.com" + DEVICES_ENDPOINT, payload=fixture)
            await ring.async_update_devices()
            mocked.assert_called_once()
        families = ring.devices()
        assert len(families.chimes) == len(fixture["chimes"])
        assert len(families.doorbots) == len(fixture["doorbots"])
        assert len(families.authorized_doorbots) == len(fixture["authorized_doorbots"])
        assert len(families.stickup_cams) == len(fixture["stickup_cams"])
        assert len(families.other) == sum(item["kind"] in INTERCOM_KINDS for item in fixture["other"])
        for family, devices in fixture.items():
            for raw in devices:
                if family == "other" and raw["kind"] not in INTERCOM_KINDS:
                    continue
                device = next(item for item in families[family] if item.id == raw["id"])
                assert device is not None
                assert device.name == raw["description"]
                assert device.kind == raw["kind"]
                assert device.device_id == raw["device_id"]
                assert device.address == raw.get("address")
                assert device.timezone == raw.get("time_zone")
                assert device.firmware == raw.get("firmware_version")
                assert device.latitude == raw.get("latitude")
                assert device.longitude == raw.get("longitude")
                assert str(device) == f"{raw['description']} ({raw['kind']})"
                assert raw["description"] in repr(device)
                assert ring.get_device_by_name(device.name) is not None
        assert ring.get_device_by_api_id(-1) is None
        assert ring.get_device_by_name("unlisted") is None
        assert len(ring.get_device_list()) == sum(len(families[family]) for family in fixture)
        assert len(ring.video_devices()) == (
            len(fixture["doorbots"])
            + len(fixture["authorized_doorbots"])
            + len(fixture["stickup_cams"])
        )
    finally:
        await auth.async_close()


@pytest.mark.asyncio
async def test_python_health_replays_shared_go_responses():
    auth, ring = legacy_ring()
    chime = ring.devices().chimes[0]
    doorbell = ring.devices().doorbots[0]
    with aioresponses() as mocked:
        mocked.get(
            "https://api.ring.com" + HEALTH_CHIMES_ENDPOINT.format(chime.id),
            payload=legacy_response("ring_chime_health_attrs.json"),
        )
        mocked.get(
            "https://api.ring.com" + HEALTH_DOORBELL_ENDPOINT.format(doorbell.id),
            payload=legacy_response("ring_doorboot_health_attrs.json"),
        )
        await chime.async_update_health_data()
        await doorbell.async_update_health_data()
        assert len(mocked.requests) == 2
    assert chime.wifi_name
    assert doorbell.wifi_name
    assert chime.wifi_signal_category
    assert doorbell.wifi_signal_strength is not None
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_history_replays_shared_go_history_fixture():
    auth, ring = legacy_ring()
    doorbell = ring.devices().doorbots[0]
    history = legacy_response("ring_doorbot_history.json")
    with aioresponses() as mocked:
        add_request(
            mocked, "GET", "https://api.ring.com" + URL_DOORBELL_HISTORY.format(doorbell.id),
            params={"limit": 30}, payload=history,
        )
        rows = await doorbell.async_history(kind="motion", convert_timezone=False)
        assert len(rows) == sum(item["kind"] == "motion" for item in history)
        assert doorbell.last_history == rows
        mocked.assert_called_once()
    assert await ring.devices().chimes[0].async_history() == []
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_controls_replay_legacy_request_shapes():
    auth, ring = legacy_ring()
    chime = ring.devices().chimes[0]
    doorbell = ring.devices().doorbots[0]
    camera = ring.devices().stickup_cams[0]
    scenarios = load_case(PORTING / "legacy-control-requests.json")
    ids = {"chime_id": chime.id, "doorbell_id": doorbell.id, "camera_id": camera.id}
    with aioresponses() as mocked:
        for scenario in scenarios:
            request = scenario["request"]
            add_request(
                mocked, request["method"], "https://api.ring.com" + request["path"].format(**ids),
                params={pair["name"]: pair["value"] for pair in request["query"]},
                body=request["body"], payload=scenario["response"]["body"],
                check_json=request["json"],
            )
        await chime.async_set_volume(2)
        await doorbell.async_set_volume(3)
        assert await chime.async_test_sound("ding") is True
        await camera.async_set_lights("on")
        await camera.async_set_siren(0)
        await doorbell.async_set_motion_detection(True)
        assert len(mocked.requests) == len(scenarios)
    with pytest.raises(RingError):
        await chime.async_set_volume(-1)
    with pytest.raises(RingError):
        await doorbell.async_set_volume(-1)
    with pytest.raises(RingError):
        await camera.async_set_lights("invalid")
    with pytest.raises(RingError):
        await camera.async_set_siren(-1)
    with pytest.raises(RingError):
        await doorbell.async_set_motion_detection("invalid")
    assert await chime.async_test_sound("not-a-sound") is False
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_recording_replays_media_fixture(tmp_path):
    auth, ring = legacy_ring()
    doorbell = ring.devices().doorbots[0]
    fixture = load_case(PORTING / "media-variants.json")["recording"]
    recording_id = 987654321
    url = "https://api.ring.com" + URL_RECORDING.format(recording_id)
    share = "https://api.ring.com" + URL_RECORDING_SHARE_PLAY.format(recording_id)
    with aioresponses() as mocked:
        mocked.get(url, body=fixture["body_text"])
        mocked.get(share, payload={"url": fixture["share_url"]})
        assert await doorbell.async_recording_download(recording_id) == fixture["body_text"].encode()
        assert await doorbell.async_recording_url(recording_id) == fixture["share_url"]
        assert len(mocked.requests) == 2
    filename = tmp_path / "clip.mp4"
    with aioresponses() as mocked:
        mocked.get(url, body=fixture["body_text"])
        assert await doorbell.async_recording_download(recording_id, str(filename)) is None
    assert filename.read_bytes() == fixture["body_text"].encode()
    with aioresponses() as mocked:
        mocked.get(url, body=fixture["body_text"])
        with pytest.raises(RingError):
            await doorbell.async_recording_download(recording_id, str(filename))
    doorbell._ring.devices_data["doorbots"][doorbell.id]["features"]["show_recordings"] = False
    assert await doorbell.async_recording_download(recording_id) is None
    assert await doorbell.async_recording_url(recording_id) is None
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_snapshot_replays_timestamp_and_image_fixture(monkeypatch, tmp_path):
    auth, ring = legacy_ring()
    doorbell = ring.devices().doorbots[0]
    fixture = load_case(PORTING / "media-variants.json")["snapshot"]
    timestamp_url = "https://api.ring.com" + SNAPSHOT_TIMESTAMP_ENDPOINT
    image_url = "https://api.ring.com" + SNAPSHOT_ENDPOINT.format(doorbell.id)

    async def no_delay(_):
        return None

    monkeypatch.setattr("ring_doorbell.doorbot.asyncio.sleep", no_delay)
    with aioresponses() as mocked:
        mocked.post(timestamp_url, payload={})
        mocked.post(timestamp_url, payload={"timestamps": [{"timestamp": fixture["timestamp_ms"]}]})
        mocked.get(image_url, body=fixture["body_text"])
        assert await doorbell.async_get_snapshot(retries=1, delay=0) == fixture["body_text"].encode()
    filename = tmp_path / "snapshot.jpg"
    with aioresponses() as mocked:
        mocked.post(timestamp_url, payload={})
        mocked.post(timestamp_url, payload={"timestamps": [{"timestamp": fixture["timestamp_ms"]}]})
        mocked.get(image_url, body=fixture["body_text"])
        assert await doorbell.async_get_snapshot(retries=1, delay=0, filename=str(filename)) is None
    assert filename.read_bytes() == fixture["body_text"].encode()
    with aioresponses() as mocked:
        mocked.post(timestamp_url, payload={})
        mocked.post(timestamp_url, payload={"timestamps": [{"timestamp": 1}]})
        assert await doorbell.async_get_snapshot(retries=1, delay=0) is None
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_history_replay_enforced_limits_and_timezone():
    auth, ring = legacy_ring()
    doorbell = ring.devices().doorbots[0]
    history = legacy_response("ring_doorbot_history.json")
    url = "https://api.ring.com" + URL_DOORBELL_HISTORY.format(doorbell.id)
    with aioresponses() as mocked:
        add_request(mocked, "GET", url, params={"limit": 1}, payload=history)
        one = await doorbell.async_history(limit=1, kind="motion", enforce_limit=True, timezone="America/New_York")
        assert len(one) == 1
        assert one[0]["created_at"].tzinfo is not None
    with aioresponses() as mocked:
        add_request(mocked, "GET", url, params={"limit": 2}, payload=history)
        add_request(mocked, "GET", url, params={"limit": 4}, payload=history)
        one = await doorbell.async_history(limit=2, kind="ding", enforce_limit=True, retry=2, convert_timezone=False)
        assert len(one) == 1
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_session_registration_replays_legacy_response():
    auth, ring = legacy_ring()
    ring.session = None
    session_fixture = legacy_response("ring_session.json")
    with aioresponses() as mocked:
        mocked.post("https://api.ring.com" + NEW_SESSION_ENDPOINT, payload=session_fixture)
        mocked.get("https://api.ring.com" + DEVICES_ENDPOINT, payload=legacy_response("ring_devices.json"))
        await ring.async_update_devices()
        assert ring.session == session_fixture
        assert len(mocked.requests) == 2
    ring.session = None
    with aioresponses() as mocked:
        mocked.post("https://api.ring.com" + NEW_SESSION_ENDPOINT, payload=session_fixture)
        mocked.get("https://api.ring.com" + DEVICES_ENDPOINT, payload=legacy_response("ring_devices.json"))
        response = await ring.async_query(DEVICES_ENDPOINT)
        assert response.json() == legacy_response("ring_devices.json")
        assert len(mocked.requests) == 2
    await auth.async_close()


@pytest.mark.asyncio
async def test_python_in_home_chime_replays_shared_control_cases():
    auth, ring = legacy_ring()
    owned, shared = ring.devices().doorbots[0], ring.devices().authorized_doorbots[0]
    scenarios = load_case(PORTING / "legacy-in-home-chime.json")
    with aioresponses() as mocked:
        for scenario in scenarios:
            request = scenario["request"]
            device = owned if scenario["case"] == "type" else shared
            add_request(
                mocked, request["method"],
                "https://api.ring.com" + request["path"].format(doorbell_id=device.id),
                params=request["query"], payload=scenario["response"]["body"],
            )
        await owned.async_set_existing_doorbell_type(1)
        await shared.async_set_existing_doorbell_type_enabled(False)
        await shared.async_set_existing_doorbell_type_duration(5)
        assert len(mocked.requests) == len(scenarios)
    with pytest.raises(RingError):
        await owned.async_set_existing_doorbell_type(99)
    with pytest.raises(RingError):
        await shared.async_set_existing_doorbell_type_enabled(1)
    with pytest.raises(RingError):
        await shared.async_set_existing_doorbell_type_duration(999)
    await auth.async_close()
