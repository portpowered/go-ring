"""Measure fixture-only Python coverage for the behavior selected for Go porting.

Run with the JSON file emitted by pytest-cov. The explicit method list is a
coverage denominator, not a list of capture files or capture metadata.
"""

import ast
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "reference" / "python-ring-doorbell" / "ring_doorbell"

# CLI, Firebase push, groups, intercom, and authentication exchange are outside
# the selected port scope. PTZ is absent from this Python library and is covered
# by recording protocol tests instead of misrepresented as Python code coverage.
METHODS = {
    "auth.py": {"Auth.async_query"},
    "ring.py": {
        "Ring.async_create_session", "Ring.async_update_devices", "Ring.async_query",
        "Ring._async_query", "Ring.devices", "Ring.get_device_list",
        "Ring.get_device_by_api_id", "Ring.video_devices", "RingDevices.__init__",
    },
    "generic.py": {"RingGeneric.async_history"},
    "doorbot.py": {
        "RingDoorBell.async_update_health_data", "RingDoorBell.async_set_volume",
        "RingDoorBell.async_set_motion_detection", "RingDoorBell.async_get_snapshot",
        "RingDoorBell.async_recording_download", "RingDoorBell.async_recording_url",
        "RingDoorBell.async_set_existing_doorbell_type",
        "RingDoorBell.async_set_existing_doorbell_type_enabled",
        "RingDoorBell.async_set_existing_doorbell_type_duration",
    },
    "chime.py": {"RingChime.async_update_health_data", "RingChime.async_set_volume", "RingChime.async_test_sound"},
    "stickup_cam.py": {"RingStickUpCam.async_set_lights", "RingStickUpCam.async_set_siren"},
    "webrtcstream.py": {"RingWebRtcStream." + name for name in (
        "__init__", "get_sdp_session_id", "generate", "_generate", "_activate",
        "on_ice_candidate", "keep_alive", "get_session_message", "reader", "pinger",
        "handle_ice_message", "handle_answer_message", "handle_close_message",
        "force_correct_sdp_answer", "insert_ice_candidates", "sync_close", "close",
        "_close", "handle_message",
    )},
}


def measure(report: dict) -> tuple[int, int, list[tuple[str, int, int, list[int]]]]:
    covered = total = 0
    details = []
    for filename, wanted in METHODS.items():
        path = SOURCE / filename
        tree = ast.parse(path.read_text(encoding="utf-8"))
        type_only = set()
        for guard in ast.walk(tree):
            if isinstance(guard, ast.If) and isinstance(guard.test, ast.Name) and guard.test.id == "TYPE_CHECKING":
                for statement in guard.body:
                    type_only.update(range(statement.lineno, statement.end_lineno + 1))
        found = {}
        for node in tree.body:
            if isinstance(node, ast.ClassDef):
                for method in node.body:
                    if isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef)):
                        found[f"{node.name}.{method.name}"] = range(method.lineno, method.end_lineno + 1)
        assert wanted <= found.keys(), f"missing Python methods in {filename}: {wanted - found.keys()}"
        file_data = next(value for key, value in report["files"].items() if key.replace("\\", "/").endswith("/" + filename))
        hits, misses = set(file_data["executed_lines"]), set(file_data["missing_lines"])
        for method in sorted(wanted):
            lines = (set(found[method]) & (hits | misses)) - type_only
            absent = sorted(lines & misses)
            covered += len(lines - misses)
            total += len(lines)
            details.append((f"{filename}:{method}", len(lines - misses), len(lines), absent))
    return covered, total, details


if __name__ == "__main__":
    data = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    covered, total, details = measure(data)
    for name, hits, lines, missing in details:
        if missing:
            print(f"{name}: {hits}/{lines}, missing {missing}")
    print(f"Selected Python port code: {covered}/{total} = {100 * covered / total:.2f}% (target 95%)")
    sys.exit(0 if covered / total >= .95 else 1)
