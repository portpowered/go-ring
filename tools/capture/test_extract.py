import json
import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from extract import Sanitizer, OUT


class SanitizerTests(unittest.TestCase):
    def test_redacts_identifiers_and_sdp_secrets_without_changing_enums(self):
        value = {
            "method": "PTZ.Pan.Step",
            "dialog_id": "dialog-private",
            "body": {
                "doorbot_id": 987654321,
                "session_id": "signaling-private",
                "command": {
                    "id": "command-private",
                    "method": "PTZ.Pan.Step",
                    "params": {"sessionId": "control-private", "direction": "LEFT"},
                },
                "notification": {"text": "camera_connected", "email": "person@example.invalid", "latitude": 37.7},
                "sdp": "v=0\r\na=ice-ufrag:private\r\na=ice-pwd:secretsecretsecretsecretsecret\r\na=candidate:1 1 UDP 1 10.2.3.4 1234 typ host\r\na=fingerprint:sha-256 AA:BB\r\n",
            },
        }
        sanitized = Sanitizer().value(value)
        self.assertEqual(sanitized["method"], "PTZ.Pan.Step")
        self.assertEqual(sanitized["body"]["command"]["params"]["direction"], "LEFT")
        self.assertEqual(sanitized["body"]["doorbot_id"], 1000)
        self.assertEqual(sanitized["dialog_id"], "dialog-1")
        self.assertNotIn("private", json.dumps(sanitized))
        self.assertNotIn("10.2.3.4", json.dumps(sanitized))
        self.assertIn("syntheticufrag", sanitized["body"]["sdp"])
        self.assertIn("192.0.2.1", sanitized["body"]["sdp"])
        self.assertIn("a=fingerprint:sha-256 " + ":".join(["00"] * 32), sanitized["body"]["sdp"])
        self.assertEqual(sanitized["body"]["notification"]["text"], "camera_connected")
        self.assertEqual(sanitized["body"]["notification"]["latitude"], 0.0)
        self.assertNotIn("person@example.invalid", json.dumps(sanitized))

    def test_sanitization_is_repeatable_and_keeps_separate_id_domains(self):
        value = {"session_id": "same", "dialog_id": "same", "riid": "same", "id": "same"}
        first = json.dumps(Sanitizer().value(value), sort_keys=True)
        second = json.dumps(Sanitizer().value(value), sort_keys=True)
        self.assertEqual(first, second)
        decoded = json.loads(first)
        self.assertEqual(len(set(decoded.values())), 4)


class FixtureTests(unittest.TestCase):
    def test_checked_fixtures_have_no_obvious_personal_network_values(self):
        ipv4 = re.compile(r"(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])")
        email = re.compile(r"[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}")
        uuid = re.compile(r"\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b", re.I)
        files = list(OUT.glob("http/*.json")) + list(OUT.glob("sessions/*.json"))
        self.assertGreaterEqual(len(files), 10)
        for path in files:
            content = path.read_text(encoding="utf-8-sig")
            for found in ipv4.findall(content):
                self.assertIn(found, {"0.0.0.0", "192.0.2.1"}, path.name)
            self.assertIsNone(email.search(content), path.name)
            self.assertIsNone(uuid.search(content), path.name)

    def test_session_recordings_keep_full_conversation_shapes(self):
        for flow, expected in ((21, 254), (402, 243)):
            data = json.loads((OUT / "sessions" / f"flow-{flow}.json").read_text(encoding="utf-8-sig"))
            self.assertEqual(len(data["messages"]), expected)
            self.assertTrue(all("direction" in row and "frame" in row and "payload" in row for row in data["messages"]))


if __name__ == "__main__":
    unittest.main()
