"""Validate recorded wire payloads, not just operation names, against API schemas."""
import copy
import json
import unittest
from pathlib import Path

import jsonschema
import yaml

ROOT = Path(__file__).resolve().parents[2]


def document(name):
    return yaml.safe_load((ROOT / "api" / name).read_text(encoding="utf-8"))


def validator(doc, schema):
    # All component references are local. Never resolve arbitrary network URIs.
    def check_refs(value):
        if isinstance(value, dict):
            if "$ref" in value and not value["$ref"].startswith("#/"):
                raise ValueError("remote schema references are not allowed")
            for item in value.values():
                check_refs(item)
        elif isinstance(value, list):
            for item in value:
                check_refs(item)
    check_refs(doc)
    root = {"components": doc["components"], **schema}
    jsonschema.Draft202012Validator.check_schema(root)
    return jsonschema.Draft202012Validator(root)


class SignalingContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = document("asyncapi.yaml")
        cls.validators = {
            direction: validator(cls.doc, {"$ref": "#/components/schemas/" + schema})
            for direction, schema in [("client_to_server", "ClientEnvelope"), ("server_to_client", "ServerEnvelope")]
        }
        cls.messages = []
        for path in sorted((ROOT / "test/recordings/sessions").glob("*.json")):
            cls.messages.extend(json.loads(path.read_text(encoding="utf-8"))["messages"])

    def test_all_recorded_payloads(self):
        self.assertEqual(len(self.messages), 497)
        for i, message in enumerate(self.messages):
            with self.subTest(index=i, method=message["payload"]["method"]):
                self.validators[message["direction"]].validate(message["payload"])

    def test_ptz_negative_variants_rejected(self):
        continuous = next(row for row in self.messages if row["direction"] == "client_to_server" and
                          row["payload"].get("body", {}).get("command", {}).get("method") == "PTZ.Pan.Continuous")
        base = continuous["payload"]
        variants = []
        value = copy.deepcopy(base); del value["body"]["command"]["params"]["speed"]; variants.append(value)
        value = copy.deepcopy(base); value["body"]["command"]["params"]["direction"] = "UP"; variants.append(value)
        value = copy.deepcopy(base); value["body"]["command"]["jsonrpc"] = "1.0"; variants.append(value)
        value = copy.deepcopy(base); del value["body"]["session_id"]; variants.append(value)
        value = copy.deepcopy(base); value["body"]["command"]["params"]["version"] = "1"; variants.append(value)
        for value in variants:
            self.assertFalse(self.validators["client_to_server"].is_valid(value))

    def test_rpc_result_and_error_are_exclusive(self):
        result = next(row["payload"] for row in self.messages if row["direction"] == "server_to_client" and
                      "result" in row["payload"].get("body", {}).get("command", {}))
        value = copy.deepcopy(result)
        value["body"]["command"]["error"] = {"code": -32602, "message": "synthetic error"}
        self.assertFalse(self.validators["server_to_client"].is_valid(value))
        del value["body"]["command"]["result"]
        self.assertTrue(self.validators["server_to_client"].is_valid(value))


if __name__ == "__main__":
    unittest.main()
