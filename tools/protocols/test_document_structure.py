"""Validate complete API documents with their maintained official validators."""
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml
from openapi_spec_validator import validate
from openapi_spec_validator.validation.exceptions import OpenAPIValidationError

ROOT = Path(__file__).resolve().parents[2]
PROTOCOL_TOOLS = ROOT / "tools" / "protocols"


def load(name):
    return yaml.safe_load((ROOT / "api" / name).read_text(encoding="utf-8"))


def reject_remote_refs(value):
    if isinstance(value, dict):
        ref = value.get("$ref")
        if ref is not None and not ref.startswith("#/"):
            raise AssertionError(f"network or external reference is not permitted: {ref}")
        for child in value.values():
            reject_remote_refs(child)
    elif isinstance(value, list):
        for child in value:
            reject_remote_refs(child)


class FullDocumentStructure(unittest.TestCase):
    def test_openapi_document(self):
        doc = load("openapi.yaml")
        reject_remote_refs(doc)
        validate(doc)

    def test_public_model_projection_document(self):
        doc = load("client-models.openapi.yaml")
        reject_remote_refs(doc)
        validate(doc)

    def test_openapi_validator_rejects_missing_required_document_fields(self):
        doc = load("openapi.yaml")
        del doc["info"]
        with self.assertRaises(OpenAPIValidationError):
            validate(doc)

    def test_asyncapi_document(self):
        doc = load("asyncapi.yaml")
        reject_remote_refs(doc)
        result = subprocess.run(
            ["node", str(PROTOCOL_TOOLS / "validate_asyncapi.mjs"), str(ROOT / "api" / "asyncapi.yaml")],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_asyncapi_parser_rejects_invalid_version(self):
        doc = load("asyncapi.yaml")
        doc["asyncapi"] = "not-an-asyncapi-version"
        with tempfile.TemporaryDirectory() as temp_dir:
            path = Path(temp_dir) / "invalid.yaml"
            path.write_text(yaml.safe_dump(doc), encoding="utf-8")
            result = subprocess.run(
                ["node", str(PROTOCOL_TOOLS / "validate_asyncapi.mjs"), str(path)],
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
            )
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
