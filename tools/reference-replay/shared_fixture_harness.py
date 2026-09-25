"""Run the committed HTTP recordings through the pinned Python HTTP client.

The JSON files under test/recordings are language-neutral inputs. Go tests can
load the same files without depending on this Python adapter.
"""

import json
import re
from contextlib import asynccontextmanager
from pathlib import Path
from typing import Any

from aioresponses import CallbackResult, aioresponses

ROOT = Path(__file__).resolve().parents[2]
HTTP = ROOT / "test" / "recordings" / "http"
SESSIONS = ROOT / "test" / "recordings" / "sessions"
PORTING = ROOT / "test" / "porting-fixtures"
IDENTITIES = {"device_id": "101", "location_id": "location-1", "recording_id": "recording-1"}


def http_cases() -> list[Path]:
    """Include the main exchanges and every captured variant."""
    return sorted(HTTP.rglob("*.json"))


def load_case(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def session_conversations() -> list[tuple[Path, dict[str, Any], list[dict[str, Any]]]]:
    """Select each captured live-view offer and its ordered peer messages."""
    conversations = []
    for path in sorted(SESSIONS.glob("*.json")):
        messages = load_case(path)["messages"]
        for index, item in enumerate(messages):
            payload = item["payload"]
            if item["direction"] != "client_to_server" or payload.get("method") != "live_view":
                continue
            dialog = payload["dialog_id"]
            replies = [
                message["payload"]
                for message in messages[index + 1 :]
                if message["direction"] == "server_to_client"
                and message["payload"].get("dialog_id") == dialog
            ]
            conversations.append((path, payload, replies))
    return conversations


def session_variants() -> list[dict[str, Any]]:
    """Small synthetic messages for branches absent from the capture."""
    return load_case(PORTING / "session-variants.json")


def request_url(case: dict[str, Any]) -> str:
    path = case["request"]["path"]
    for field, value in IDENTITIES.items():
        path = path.replace("{" + field + "}", value)
    assert "{" not in path, f"unresolved path identity: {path}"
    return case["request"]["origin"] + path


def query(case: dict[str, Any]) -> dict[str, str]:
    pairs = case["request"]["query"]
    names = [pair["name"] for pair in pairs]
    assert len(names) == len(set(names)), "Python query adapter cannot represent repeated names"
    return {pair["name"]: pair["value"] for pair in pairs}


@asynccontextmanager
async def replay_http(case: dict[str, Any]):
    """Fail if the Python client changes method, URL, query, or JSON body."""
    request, response = case["request"], case["response"]
    url, expected_query = request_url(case), query(case)
    seen: list[tuple[str, str]] = []

    def callback(actual_url, **kwargs):
        seen.append((request["method"], str(actual_url)))
        assert actual_url.path == request["path"].format(**IDENTITIES)
        assert {name: str(value) for name, value in (kwargs.get("params") or {}).items()} == expected_query
        if request["json"]:
            assert kwargs.get("json") == request["body"]
        elif request["method"] == "PUT":
            # Python deliberately sends a JSON null on older PUT controls.
            assert kwargs.get("json") is None
        else:
            assert "json" not in kwargs
            assert kwargs.get("data") is None
        body = response["body"]
        payload = {"payload": body} if response["json"] else {"body": b"" if body is None else body}
        headers = {key: values[0] for key, values in response["headers"].items()}
        return CallbackResult(status=response["status"], headers=headers, **payload)

    with aioresponses() as mocked:
        # Match query-bearing URLs with a regex: aioresponses normalizes query
        # values twice and would otherwise reject commas as %252C before our
        # callback can verify the original parameters passed by Python.
        mocked.add(re.compile(r"^" + re.escape(url) + r"(?:\?.*)?$"), method=request["method"], callback=callback)
        yield url, expected_query
        assert len(seen) == 1, f"expected one request for {request['method']} {url}; got {seen}"
        mocked.assert_called_once()
