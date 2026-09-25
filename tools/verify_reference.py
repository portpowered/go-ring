"""Run the pinned Python baseline before recording and API contract checks.

Requires uv, Node.js and npm on PATH. Creates separate ignored environments so
the capture extractor and protocol validators cannot change the locked reference.
"""
from pathlib import Path
import os
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REFERENCE = ROOT / "reference" / "python-ring-doorbell"


def run(*args, cwd=ROOT, env=None):
    subprocess.run([str(arg) for arg in args], cwd=cwd, env=env, check=True)


def interpreter(venv):
    return venv / ("Scripts/python.exe" if os.name == "nt" else "bin/python")


def main():
    if not (REFERENCE / "uv.lock").exists():
        raise SystemExit("Initialize reference/python-ring-doorbell with git submodule update --init first.")
    reference_env = ROOT / ".venv-reference"
    capture_env = ROOT / ".venv-capture"
    env = dict(os.environ, UV_PROJECT_ENVIRONMENT=str(reference_env),
               PYTHONPATH=str(ROOT / "tools" / "reference-replay"))
    run("uv", "sync", "--locked", "--python", "3.12", "--project", REFERENCE, env=env)
    python = interpreter(reference_env)
    run("uv", "pip", "install", "--python", python, "-r",
        ROOT / "tools/reference-replay/requirements.txt")
    run("uv", "pip", "install", "--python", python, "-r",
        ROOT / "tools/protocols/requirements.txt")
    npm = "npm.cmd" if os.name == "nt" else "npm"
    run(npm, "ci", "--prefix", ROOT / "tools/protocols", "--ignore-scripts")
    pytest = (python, "-m", "pytest", "-p", "no:socket", "-p",
              "offline_socket_guard", "-o", "addopts=", "-q")
    run(*pytest, "tests", cwd=REFERENCE, env=env)
    run(*pytest, ROOT / "tools/reference-replay/test_shared_recordings.py", cwd=REFERENCE, env=env)
    run(python, "-m", "unittest", "discover", "-s", "tools/protocols", "-v", env=env)
    if not interpreter(capture_env).exists():
        run("uv", "venv", "--python", "3.12", capture_env)
    run("uv", "pip", "install", "--python", interpreter(capture_env), "-r",
        ROOT / "tools/capture/requirements.txt")
    run(interpreter(capture_env), "-m", "unittest", "discover", "-s", "tools/capture", "-v")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as exc:
        sys.exit(exc.returncode)
