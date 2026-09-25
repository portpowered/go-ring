"""Replay the Python RTC subset of the captured signaling conversations."""

import json
import asyncio

import pytest
from ring_doorbell.webrtcstream import RingWebRtcStream

from shared_fixture_harness import PORTING, load_case, session_conversations, session_variants
from ring_doorbell.auth import Auth
from ring_doorbell.exceptions import RingError


class RecordingSocket:
    def __init__(self):
        self.sent = []
        self.closed = False

    async def send(self, value):
        self.sent.append(json.loads(value))

    async def close(self):
        self.closed = True


class PeerSocket(RecordingSocket):
    def __init__(self, replies):
        super().__init__()
        self.replies = replies
        self.offered = asyncio.Event()

    async def send(self, value):
        await super().send(value)
        if self.sent[-1]["method"] == "live_view":
            self.offered.set()

    async def __aiter__(self):
        await self.offered.wait()
        for reply in self.replies:
            yield json.dumps(reply)


@pytest.mark.asyncio
@pytest.mark.parametrize("path,offer,replies", session_conversations(), ids=lambda item: str(item)[:40])
async def test_python_rtc_handles_captured_peer_messages(path, offer, replies):
    """Reuse real order/SDP/ICE while exposing Python's absent PTZ support."""
    seen = []
    socket = RecordingSocket()
    session = RingWebRtcStream(
        ring=None,
        device_api_id=offer["body"]["doorbot_id"],
        on_message_callback=seen.append,
    )
    session.websocket = socket
    session.dialog_id = offer["dialog_id"]
    session.sdp_offer = offer["body"]["sdp"]
    try:
        for reply in replies:
            await session.handle_message(json.dumps(reply))
        answers = [m for m in seen if m.answer is not None]
        assert len(answers) == 1, f"{path.name} {offer['dialog_id']}"
        assert session.session_id
        assert session.sdp
        assert any(message["method"] == "activate_session" for message in socket.sent)
        assert sum(message["method"] == "activate_session" for message in socket.sent) == 1
        assert len([m for m in seen if m.candidate is not None]) == sum(
            reply.get("method") == "ice" for reply in replies
        )
    finally:
        await session.close()
    assert socket.closed


@pytest.mark.asyncio
@pytest.mark.parametrize("variant", session_variants(), ids=lambda variant: variant["case"])
async def test_python_rtc_synthetic_message_variants(variant):
    seen = []
    closed = []

    async def on_close():
        closed.append(True)

    socket = RecordingSocket()
    session = RingWebRtcStream(None, 101, on_message_callback=seen.append, on_close_callback=on_close)
    session.websocket = socket
    session.dialog_id = "dialog-1"
    session.session_id = "signal-1"
    await session.handle_message(json.dumps(variant["message"]))
    if variant["case"] == "remote-close":
        assert closed == [True]
        assert not session.is_alive
        assert seen[-1].error_code == "session_closed"
    elif variant["case"] == "camera-connected":
        assert socket.sent[-1]["method"] == "camera_options"
        assert socket.sent[-1]["body"]["stealth_mode"] is False
    else:
        assert not socket.sent
    await session.close()


@pytest.mark.asyncio
async def test_python_rtc_ice_send_and_collect_from_recorded_messages():
    _, offer, _ = session_conversations()[0]
    socket = RecordingSocket()
    session = RingWebRtcStream(None, offer["body"]["doorbot_id"])
    session.websocket = socket
    session.dialog_id = offer["dialog_id"]
    session._offered_event.set()
    ice = next(variant["message"] for variant in session_variants() if variant["case"] == "remote-ice")
    await session.on_ice_candidate("candidate:synthetic", 1)
    assert socket.sent[-1]["body"] == {
        "doorbot_id": offer["body"]["doorbot_id"],
        "ice": "candidate:synthetic",
        "mlineindex": 1,
    }
    session.session_id = "signal-1"
    await session.on_ice_candidate("candidate:second", 0)
    assert socket.sent[-1]["body"]["session_id"] == "signal-1"
    session.collect_ice_candidates = True
    session.handle_ice_message(ice)
    index = int(ice["body"]["mlineindex"])
    assert session.ice_candidates[index] == [ice["body"]["ice"]]
    await session.close()


@pytest.mark.asyncio
async def test_python_rtc_keepalive_ping_and_timeout(monkeypatch):
    socket = RecordingSocket()
    session = RingWebRtcStream(None, 101, keep_alive_timeout=30)
    session.websocket = socket
    session.dialog_id = "dialog-1"
    session.session_id = "signal-1"

    async def one_tick(_):
        session.is_alive = False

    monkeypatch.setattr("ring_doorbell.webrtcstream.asyncio.sleep", one_tick)
    await session.keep_alive()
    await session.pinger()
    assert socket.sent == [session.get_session_message("ping", {})]
    await session.close()

    expired = RingWebRtcStream(None, 101, keep_alive_timeout=0)
    expired.websocket = RecordingSocket()
    expired.dialog_id = "dialog-1"
    expired._last_keep_alive = 0
    await expired.pinger()
    assert expired.websocket.sent == []
    await expired.close()


@pytest.mark.asyncio
async def test_python_rtc_close_schedule_and_sdp_helpers():
    _, offer, _ = session_conversations()[0]
    session = RingWebRtcStream(None, 101)
    session.websocket = RecordingSocket()
    session.dialog_id = "dialog-1"
    assert session.get_sdp_session_id(offer["body"]["sdp"]) is not None
    assert session.get_sdp_session_id("invalid") is None
    session.sdp = "v=0\r\na=mid:0\r\n"
    session.ice_candidates[0] = ["candidate:01 synthetic"]
    session.insert_ice_candidates()
    assert "a=candidate:01 synthetic" in session.sdp
    assert session.collect_ice_candidates is False
    session.sync_close()
    await session._close_task
    assert session.websocket is None


@pytest.mark.asyncio
@pytest.mark.parametrize("with_callback", [False, True])
async def test_python_rtc_generate_replays_legacy_ticket_and_recorded_answer(monkeypatch, with_callback):
    _, offer, replies = session_conversations()[0]
    ticket = load_case(PORTING / "legacy-ticket.json")
    answer_replies = [reply for reply in replies if reply["method"] in {"session_created", "sdp"}]
    socket = PeerSocket(answer_replies)
    queried = []
    seen = []

    class TicketRing:
        async def async_query(self, path, **kwargs):
            queried.append((path, kwargs))
            return Auth.Response(json.dumps(ticket["response"]["body"]).encode(), 200)

    async def connect(url, **kwargs):
        assert "synthetic-legacy-ticket" in url
        assert kwargs["user_agent_header"] == "android:com.ringapp"
        return socket

    monkeypatch.setattr("ring_doorbell.webrtcstream.connect", connect)
    monkeypatch.setattr("ring_doorbell.webrtcstream.ssl.create_default_context", lambda: object())
    session = RingWebRtcStream(
        TicketRing(), offer["body"]["doorbot_id"],
        on_message_callback=seen.append if with_callback else None,
    )
    try:
        result = await session.generate(offer["body"]["sdp"])
        await session.read_task
        assert queried == [(
            ticket["request"]["path"],
            {"method": ticket["request"]["method"], "base_uri": ticket["request"]["origin"]},
        )]
        assert socket.sent[0]["method"] == "live_view"
        assert socket.sent[0]["body"]["sdp"] == offer["body"]["sdp"]
        assert any(m["method"] == "activate_session" for m in socket.sent)
        if with_callback:
            assert result is None
            assert len([m for m in seen if m.answer]) == 1
        else:
            assert result == session.sdp
    finally:
        await session.close()
    assert socket.closed


@pytest.mark.asyncio
@pytest.mark.parametrize("scenario", load_case(PORTING / "session-lifecycle.json"), ids=lambda item: item["case"])
async def test_python_rtc_lifecycle_replays_synthetic_variants(monkeypatch, scenario):
    session = RingWebRtcStream(None, 101)
    session.websocket = RecordingSocket()

    async def no_delay(_):
        return None

    monkeypatch.setattr("ring_doorbell.webrtcstream.asyncio.sleep", no_delay)
    case = scenario["case"]
    if case == "ticket-error":
        class FailedTicketRing:
            async def async_query(self, *_args, **_kwargs):
                raise RuntimeError("synthetic ticket failure")

        session._ring = FailedTicketRing()
        with pytest.raises(RingError, match="Error generating RTC stream"):
            await session._generate(scenario["offer"])
    elif case == "answer-direction":
        session.sdp_offer = scenario["offer"]
        session.sdp = scenario["answer"]
        session.force_correct_sdp_answer()
        assert "a=sendonly" in session.sdp
        assert "a=sendrecv" not in session.sdp
    else:
        session.collect_ice_candidates = True

        async def fake_generate(_offer):
            session.sdp = scenario.get("answer")
            if case == "collected-ice":
                session.ice_candidates[0] = [scenario["candidate"]]

        monkeypatch.setattr(session, "_generate", fake_generate)
        if case == "no-answer":
            with pytest.raises(RingError, match="Unable to generate RTC stream"):
                await session.generate(scenario["offer"])
        else:
            assert "a=candidate:01 synthetic" in await session.generate(scenario["offer"])
    await session.close()
