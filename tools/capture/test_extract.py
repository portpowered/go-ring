import json
import re
import sys
import unittest
from copy import deepcopy
from pathlib import Path

import jsonschema

sys.path.insert(0, str(Path(__file__).parent))
from extract import Sanitizer, OUT, safe_path


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

    def test_nested_untrusted_credentials_and_location_text_are_scrubbed(self):
        value = {
            "extra": {
                "credentials": {"access_token": "plain-secret-value", "authorization": "Bearer another-secret"},
                "where": "37.7749, -122.4194",
                "mailing_address": "42 Elm Street, Exampletown",
                "note": "api_key=private-key-value",
                "hardware_id": "hardware-serial-value",
                "timezone": "America/Los_Angeles",
                "location": "Home on Cedar Road",
            },
            "supported_rpc_commands": ["PTZ.Pan.Step", "PTZ.Tilt.Continuous"],
        }
        sanitized = Sanitizer().value(value)
        serialized = json.dumps(sanitized)
        for secret in ("plain-secret-value", "another-secret", "37.7749", "-122.4194", "Elm Street", "private-key-value", "hardware-serial-value", "America/Los_Angeles", "Cedar Road"):
            self.assertNotIn(secret, serialized)
        self.assertEqual(sanitized["supported_rpc_commands"], ["PTZ.Pan.Step", "PTZ.Tilt.Continuous"])

    def test_device_id_cross_reference_is_stable_and_versions_stay_literal(self):
        result = Sanitizer().value({"id": 709739068, "device_id": 709739068})
        self.assertEqual(result["id"], result["device_id"])
        self.assertEqual(safe_path("/devices/v1/devices/709739068/settings"), "/devices/v1/devices/{device_id}/settings")
        self.assertEqual(safe_path("/evm/v2/timeline/devices/709739068"), "/evm/v2/timeline/devices/{device_id}")


class FixtureTests(unittest.TestCase):
    def test_checked_fixtures_have_no_obvious_personal_network_values(self):
        ipv4 = re.compile(r"(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])")
        email = re.compile(r"[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}")
        uuid = re.compile(r"\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b", re.I)
        files = list(OUT.glob("http/**/*.json")) + list(OUT.glob("sessions/*.json"))
        self.assertGreaterEqual(len(files), 30)
        for path in files:
            content = path.read_text(encoding="utf-8-sig")
            for found in ipv4.findall(content):
                self.assertIn(found, {"0.0.0.0", "192.0.2.1"}, path.name)
            self.assertIsNone(email.search(content), path.name)
            self.assertIsNone(uuid.search(content), path.name)

    def test_every_fixture_validates_against_its_json_schema(self):
        http_schema = json.loads((OUT / "schemas" / "http-exchange.schema.json").read_text(encoding="utf-8-sig"))
        session_schema = json.loads((OUT / "schemas" / "session.schema.json").read_text(encoding="utf-8-sig"))
        for schema in (http_schema, session_schema):
            jsonschema.Draft202012Validator.check_schema(schema)
        for path in (OUT / "http").rglob("*.json"):
            jsonschema.validate(json.loads(path.read_text(encoding="utf-8-sig")), http_schema)
        for path in (OUT / "sessions").glob("*.json"):
            jsonschema.validate(json.loads(path.read_text(encoding="utf-8-sig")), session_schema)

    def test_json_schema_pins_null_array_and_identity_types(self):
        schema = json.loads((OUT / "schemas" / "shape-regression.schema.json").read_text(encoding="utf-8-sig"))
        jsonschema.Draft202012Validator.check_schema(schema)
        good = {"nullable_value": None, "items": [], "device_id": 1000, "session_id": "session-1", "dialog_id": "dialog-1", "command_id": "command-1"}
        jsonschema.validate(good, schema)
        for key, value in (("nullable_value", []), ("items", None), ("device_id", "1000"), ("session_id", 1000), ("dialog_id", 1000), ("command_id", 1000)):
            bad = deepcopy(good)
            bad[key] = value
            with self.assertRaises(jsonschema.ValidationError):
                jsonschema.validate(bad, schema)

    def test_distinct_operation_variants_are_present(self):
        variants = list((OUT / "http" / "variants").glob("*.json"))
        self.assertGreaterEqual(len(variants), 10)
        names = {path.name for path in variants}
        self.assertTrue(any(name.startswith("device-settings-patch-") for name in names))
        self.assertTrue(any(name.startswith("device-detail-") for name in names))
        self.assertTrue(any(name.startswith("device-timeline-") for name in names))

    def test_device_rpc_capabilities_keep_protocol_method_names(self):
        fixture = json.loads((OUT / "http" / "device-list.json").read_text(encoding="utf-8-sig"))
        arrays = []

        def visit(value):
            if isinstance(value, dict):
                for key, child in value.items():
                    if key == "supported_rpc_commands":
                        arrays.append(child)
                    visit(child)
            elif isinstance(value, list):
                for child in value:
                    visit(child)

        visit(fixture["response"]["body"])
        self.assertTrue(arrays)
        self.assertTrue(all(isinstance(item, list) for item in arrays))
        self.assertTrue(any(command.startswith("PTZ.") for item in arrays for command in item if isinstance(command, str)))

    def test_cassettes_keep_version_segments_and_template_only_identifiers(self):
        settings = json.loads((OUT / "http" / "device-settings-patch.json").read_text(encoding="utf-8-sig"))
        self.assertEqual(settings["request"]["path"], "/devices/v1/devices/{device_id}/settings")
        for path in (OUT / "http").rglob("*.json"):
            cassette = json.loads(path.read_text(encoding="utf-8-sig"))
            self.assertNotRegex(cassette["request"]["path"], r"/(?:v1|v2|v3|v4)/\{device_id\}")

    def test_session_recordings_keep_full_conversation_shapes(self):
        for flow, expected in ((21, 254), (402, 243)):
            data = json.loads((OUT / "sessions" / f"flow-{flow}.json").read_text(encoding="utf-8-sig"))
            self.assertEqual(len(data["messages"]), expected)
            self.assertTrue(all("direction" in row and "frame" in row and "payload" in row for row in data["messages"]))


if __name__ == "__main__":
    unittest.main()
