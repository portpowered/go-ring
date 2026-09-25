"""Extract compact, sanitized HTTP and WebSocket JSON fixtures from a local mitmproxy dump.

The input capture is private and is never copied into the repository. Run with:
  python tools/capture/extract.py docs/internal/network-capture-flows-ring.mitmproxy
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from urllib.parse import urlsplit

from mitmproxy.io import FlowReader


ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "test" / "recordings"
PRIVATE_KEY = re.compile(r"token|secret|password|credential|authorization|cookie|email|phone|address|postal|post.?code|zip|latitude|longitude|coordinate|(?:^|_)(?:lat|lon|lng)$|serial|mac|bssid|ssid|fingerprint|ice.?pwd|ice.?ufrag|usernamefragment|private.?key|nonce|(?:^|_)auth(?:_|$)|(?:^|_)sid$|device.?id|doorbot.?id|location.?id|owner|user.?id|account.?id|uuid|session.?id|dialog.?id|riid|command.?id|ticket|cell.?id|ding.?id|ip.?address|(?:^|_)ip$|(?:^|_)id$|(?:^|_)(?:name|description|text|host|region|gateway)$", re.I)
SAFE_FIELDS = {"method", "jsonrpc", "direction", "reason", "type", "kind", "status", "command_name", "model", "firmware", "hardware_id", "device_type", "device_family", "protocol", "content_type", "codec", "mid", "setup", "fingerprint_type", "network_type", "candidate_type", "sdp_type", "version", "source", "event", "event_type", "notification_type", "notification_scope", "source_type", "action", "role", "state", "timezone"}
SAFE_TEXT_ENUMS = {"camera_connected"}
PRIVATE_TEXT = re.compile(r"(?:[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}|\b\+?\d[\d ()-]{7,}\d\b|\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b|\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b|https?://[^\s\"']+)", re.I)


class Sanitizer:
    def __init__(self) -> None:
        self.ids: dict[tuple[str, str], str] = {}
        self.numeric_ids: dict[tuple[str, str], int] = {}
        self.times: dict[int, int] = {}

    def identifier(self, key: str, value: str) -> str:
        # Separate protocol identity domains while keeping repeated references stable.
        domain = re.sub(r"[^a-z]", "", key.lower())
        if "dialog" in domain: prefix = "dialog"
        elif "riid" in domain or "route" in domain: prefix = "route"
        elif "command" in domain or domain in {"id", "requestid"}: prefix = "command"
        elif "session" in domain: prefix = "session"
        elif "device" in domain or "doorbot" in domain: prefix = "device"
        elif "location" in domain: prefix = "location"
        elif "ticket" in domain: prefix = "ticket"
        else: prefix = "opaque"
        token = (prefix, value)
        if token not in self.ids:
            self.ids[token] = f"{prefix}-{sum(1 for p, _ in self.ids if p == prefix) + 1}"
        return self.ids[token]

    def numeric_identifier(self, key: str, value: int) -> int:
        domain = re.sub(r"[^a-z]", "", key.lower())
        prefix = "device" if ("device" in domain or "doorbot" in domain) else "user" if "user" in domain else "location" if "location" in domain else "opaque"
        token = (prefix, str(value))
        if token not in self.numeric_ids:
            self.numeric_ids[token] = 1000 + sum(1 for p, _ in self.numeric_ids if p == prefix)
        return self.numeric_ids[token]

    def value(self, value, key=""):
        if isinstance(value, dict):
            return {k: self.value(v, "device_id" if k == "id" and "device_id" in value else k) for k, v in value.items()}
        if isinstance(value, list):
            return [self.value(v, key) for v in value]
        if isinstance(value, str):
            if key.lower() == "text" and value in SAFE_TEXT_ENUMS:
                return value
            if self.is_time_key(key):
                return "2026-01-01T00:00:00Z" if re.search(r"[-T:]", value) else "1700000000000"
            if PRIVATE_KEY.search(key) and key.lower() not in SAFE_FIELDS:
                return self.identifier(key, value)
            if key.lower() in {"id", "requestid"}:
                return self.identifier(key, value)
            if key.lower() in {"sdp", "description"} and ("v=0" in value or "a=ice-" in value):
                return self.sdp(value)
            if key.lower() in {"candidate", "candidates", "ice"} and " typ " in value:
                return self.ice_candidate(value)
            if PRIVATE_TEXT.search(value) and key.lower() not in SAFE_FIELDS:
                return "sanitized-text"
        if isinstance(value, int) and not isinstance(value, bool) and PRIVATE_KEY.search(key) and key.lower() not in SAFE_FIELDS:
            return self.numeric_identifier(key, value)
        if isinstance(value, float) and PRIVATE_KEY.search(key) and key.lower() not in SAFE_FIELDS:
            return 0.0
        if isinstance(value, int) and not isinstance(value, bool) and self.is_time_key(key):
            if value not in self.times:
                base = 1_700_000_000 if value < 100_000_000_000 else 1_700_000_000_000
                self.times[value] = base + len(self.times)
            return self.times[value]
        return value

    @staticmethod
    def is_time_key(key: str) -> bool:
        return key.lower() in {"timestamp", "time", "date"} or bool(re.search(r"(?:_time|_at)$", key.lower()))

    def sdp(self, s: str) -> str:
        out = []
        for line in s.splitlines():
            if line.startswith("o="):
                fields = line.split()
                if len(fields) >= 6:
                    line = f"o=- 1 1 IN {fields[4]} {'0.0.0.0' if fields[4] == 'IP4' else '::'}"
            elif line.startswith("a=ice-ufrag:"): line = "a=ice-ufrag:syntheticufrag"
            elif line.startswith("a=ice-pwd:"): line = "a=ice-pwd:syntheticicepassword0123456789"
            elif line.startswith("a=fingerprint:"):
                alg = line.split(":", 1)[1].split(" ", 1)[0]
                width = 32 if alg.lower() == "sha-256" else 20
                line = "a=fingerprint:" + alg + " " + ":".join(["00"] * width)
            elif line.startswith("a=candidate:"): line = self.ice_candidate(line)
            elif line.startswith(("c=IN IP4 ", "c=IN IP6 ")):
                line = line.rsplit(" ", 1)[0] + (" 0.0.0.0" if "IP4" in line else " ::")
            elif line.startswith("a=ssrc:"):
                line = re.sub(r"a=ssrc:\d+", "a=ssrc:123456", line)
                line = re.sub(r"cname:[^ ]+", "cname:syntheticcname", line)
            elif line.startswith("a=msid:"):
                line = "a=msid:syntheticstream synthetictrack"
            if line.startswith(("o=", "c=", "a=candidate:", "a=rtcp:", "a=remote-candidates:")):
                line = re.sub(r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])", "192.0.2.1", line)
                line = re.sub(r"(?i)(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+", "2001:db8::1", line)
                line = re.sub(r"(?i)\b[a-z0-9-]+\.local\b", "synthetic.local", line)
            line = re.sub(r"(?i)\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b", "synthetic-id", line)
            out.append(line)
        return "\r\n".join(out) + ("\r\n" if s.endswith(("\n", "\r")) else "")

    def ice_candidate(self, s: str) -> str:
        s = re.sub(r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])", "192.0.2.1", s)
        s = re.sub(r"(?i)(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+", "2001:db8::1", s)
        s = re.sub(r"(?i)\b[a-z0-9-]+\.local\b", "synthetic.local", s)
        return re.sub(r"(?:[0-9a-f]{2}:){5}[0-9a-f]{2}", "00:00:00:00:00:00", s, flags=re.I)


def json_body(message) -> tuple[bool, object | None]:
    if not message or not message.content:
        return False, None
    try:
        return True, json.loads(message.get_text(strict=False))
    except (ValueError, UnicodeDecodeError) as exc:
        raise ValueError("selected API body is not valid JSON") from exc


def safe_path(path: str) -> str:
    parts = urlsplit(path)
    p = parts.path
    p = re.sub(r"(?<=devices/)[^/]+(?=/settings|$)", "{device_id}", p)
    p = re.sub(r"(?<=devices/)[^/]+(?=/|$)", "{device_id}", p)
    p = re.sub(r"(?<=locations/)[^/]+(?=/|$)", "{location_id}", p)
    p = re.sub(r"(?<=doorbots/)[^/]+(?=/|$)", "{device_id}", p)
    return p


def eligible(flow) -> str | None:
    req = flow.request
    h, p = req.pretty_host, urlsplit(req.path).path
    if h == "api.ring.com":
        if p == "/device_info/v3/devices": return "device-list"
        if re.fullmatch(r"/device_info/v3/devices/[^/]+", p): return "device-detail"
        if p.endswith("/settings") and p.startswith("/devices/v1/devices/"): return "device-settings-" + req.method.lower()
        if re.fullmatch(r"/clients_api/doorbots/[^/]+/siren_(on|off)", p): return "siren-" + p.rsplit("_", 1)[-1]
        if p == "/evm/v3/history/devices": return "history-devices"
        if re.fullmatch(r"/evm/v2/timeline/devices/[^/]+", p): return "device-timeline"
    if h == "prd-api-us.prd.rings.solutions" and p == "/api/v1/clap/tickets": return "bootstrap-ticket"
    return None


def extract(source: Path) -> None:
    flows = []
    with source.open("rb") as stream:
        flows = list(FlowReader(stream).stream())
    http_records: dict[str, dict] = {}
    sanitizer = Sanitizer()
    for flow in flows:
        if not flow.request or not flow.response:
            continue
        name = eligible(flow)
        if not name:
            continue
        if name in http_records:
            continue
        req, res = flow.request, flow.response
        req_is_json, req_body = json_body(req)
        res_is_json, res_body = json_body(res)
        query = [{"name": name, "value": sanitizer.value(value, name)} for name, value in req.query.items(multi=True)]
        # Store only useful JSON API exchanges; query/header values are deliberately omitted.
        http_records[name] = {
            "request": {"method": req.method, "origin": "https://" + req.pretty_host, "path": safe_path(req.path), "query": query, "headers": {"Accept": [req.headers.get("accept", "application/json")]}, "headers_mode": "required", "body": sanitizer.value(req_body) if req_is_json else None, "json": req_is_json},
            "response": {"status": res.status_code, "headers": {"Content-Type": ["application/json"]} if res_is_json else {}, "body": sanitizer.value(res_body) if res_is_json else None, "json": res_is_json},
        }
    for name, record in http_records.items():
        write_json(OUT / "http" / f"{name}.json", record)

    for flow_no in (21, 402):
        flow = flows[flow_no - 1]
        if not flow.websocket:
            raise ValueError(f"expected WebSocket flow {flow_no}")
        sanitizer = Sanitizer()
        messages = []
        for message in flow.websocket.messages:
            direction = "client_to_server" if message.from_client else "server_to_client"
            raw = message.content
            if message.type.name != "TEXT":
                raise ValueError(f"unsupported non-text frame in selected session flow {flow_no}")
            try:
                payload = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, ValueError) as exc:
                raise ValueError(f"unsupported non-JSON text in selected session flow {flow_no}") from exc
            payload = sanitizer.value(payload)
            frame = "text"
            messages.append({"direction": direction, "frame": frame, "payload": payload})
        write_json(OUT / "sessions" / f"flow-{flow_no}.json", {"messages": messages})


def write_json(path: Path, value) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("capture", type=Path)
    extract(parser.parse_args().capture)
