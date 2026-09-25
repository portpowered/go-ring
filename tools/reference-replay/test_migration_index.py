"""Keep the upstream Python-to-Go migration index complete as tests evolve."""

import ast

from shared_fixture_harness import ROOT


def test_every_original_python_test_has_a_migration_decision():
    tests = ROOT / "reference" / "python-ring-doorbell" / "tests"
    documented = (ROOT / "docs" / "python-replay-harness.md").read_text(encoding="utf-8")
    expected = set()
    for path in tests.glob("test_*.py"):
        tree = ast.parse(path.read_text(encoding="utf-8"))
        expected.update(
            f"{path.stem}::{node.name}"
            for node in tree.body
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
            and node.name.startswith("test_")
        )
    listed = {
        line.split("`")[1]
        for line in documented.splitlines()
        if line.startswith("| `test_")
    }
    assert listed == expected, f"missing={expected - listed}; obsolete={listed - expected}"
