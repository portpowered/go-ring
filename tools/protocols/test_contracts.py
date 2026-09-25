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
        for path in sorted((ROOT / "tests/replay/fixtures/recordings/sessions").glob("*.json")):
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


class HTTPContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = document("openapi.yaml")
        cls.rows = [(path, json.loads(path.read_text(encoding="utf-8")))
                    for path in sorted((ROOT / "tests/replay/fixtures/recordings/http").rglob("*.json"))]

    def resolve(self, value):
        while "$ref" in value:
            ref = value["$ref"]
            self.assertTrue(ref.startswith("#/"))
            value = self.doc
            for part in ref[2:].split("/"):
                value = value[part]
        return value

    def test_recorded_http_bodies(self):
        for path, row in self.rows:
            with self.subTest(fixture=path.name):
                request, response = row["request"], row["response"]
                operation = self.doc["paths"][request["path"]][request["method"].lower()]
                servers = operation.get("servers", self.doc["servers"])
                self.assertIn(request["origin"], [server["url"] for server in servers])
                body_contract = self.resolve(operation.get("requestBody", {}))
                if body_contract.get("required"):
                    self.assertTrue(request["json"])
                if request["json"]:
                    schema = body_contract["content"]["application/json"]["schema"]
                    validator(self.doc, schema).validate(request["body"])
                response_contract = self.resolve(operation["responses"][str(response["status"])])
                if response["json"]:
                    schema = response_contract["content"]["application/json"]["schema"]
                    validator(self.doc, schema).validate(response["body"])
                else:
                    self.assertIsNone(response["body"])
                    self.assertNotIn("content", response_contract)

    def test_synthetic_invalid_http_payloads_rejected(self):
        # These mutations are robustness cases, not additional captured replies.
        cases = [
            ("/commands/v1/devices/{device_id}", "patch", {"command_name": "unknown"}),
            ("/duos/v1/devices/{device_id}/update", "put", {"entity": {"live_view_enabled": "false"}}),
        ]
        for path, method, body in cases:
            contract = self.resolve(self.doc["paths"][path][method]["requestBody"])
            schema = contract["content"]["application/json"]["schema"]
            self.assertFalse(validator(self.doc, schema).is_valid(body))
        inventory = validator(self.doc, {"$ref": "#/components/schemas/DeviceList"})
        self.assertFalse(inventory.is_valid({"devices": {}}))
        self.assertFalse(inventory.is_valid({}))

    def test_captured_query_parameters_are_named_without_claiming_requiredness(self):
        for path, row in self.rows:
            request = row["request"]
            if not request.get("query"):
                continue
            operation = self.doc["paths"][request["path"]][request["method"].lower()]
            declared = {}
            for item in operation.get("parameters", []):
                parameter = self.resolve(item)
                declared[(parameter["in"], parameter["name"])] = parameter
            for pair in request["query"]:
                with self.subTest(fixture=path.name, parameter=pair["name"]):
                    parameter = declared.get(("query", pair["name"]))
                    self.assertIsNotNone(parameter)
                    self.assertFalse(parameter.get("required", False))

    def test_typed_settings_wire_field_and_unknown_extensions(self):
        response = validator(self.doc, {"$ref": "#/components/schemas/DeviceSettings"})
        captured = next(row["response"]["body"] for path, row in self.rows
                        if path.name == "device-settings-get.json")
        self.assertTrue(response.is_valid(captured))
        extended = copy.deepcopy(captured)
        extended["motion_settings"]["future_vendor_field"] = {"opaque": [1, "x"]}
        self.assertTrue(response.is_valid(extended))
        invalid = copy.deepcopy(captured)
        invalid["motion_settings"]["motion_detection_enabled"] = "yes"
        self.assertFalse(response.is_valid(invalid))
        missing = {"motion_settings": {}}
        null_value = {"motion_settings": {"motion_detection_enabled": None}}
        null_settings = {"motion_settings": None}
        self.assertTrue(response.is_valid(missing))
        self.assertTrue(response.is_valid(null_value))
        self.assertTrue(response.is_valid(null_settings))


if __name__ == "__main__":
    unittest.main()
