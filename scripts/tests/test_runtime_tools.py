"""Run the PowerShell tools, checking failure status and safe public output."""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
PWSH = shutil.which("pwsh")


class RuntimeToolsTest(unittest.TestCase):
    def invoke(self, script, profile, *args, expect_success=False, extra_env=None):
        self.assertIsNotNone(PWSH, "PowerShell 7 is required for script tests")
        with tempfile.TemporaryDirectory(prefix="xdb-runtime-tools-") as directory:
            path = Path(directory) / "profile.json"
            path.write_text(json.dumps(profile), encoding="utf-8")
            result = subprocess.run(
                [PWSH, "-NoProfile", "-File", str(ROOT / "scripts" / script), "-Profile", str(path), *args],
                capture_output=True, text=True, timeout=30, env={**os.environ, **(extra_env or {})},
            )
        if expect_success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, "invalid/unavailable environment was reported as ready")
        self.assertNotIn("do-not-log-this-secret", result.stdout + result.stderr)
        self.assertTrue(result.stdout.lstrip().startswith("{"), "tool does not emit the required structured failure report")
        return json.loads(result.stdout)

    def test_preflight_rejects_unisolated_or_incomplete_profile(self):
        result = self.invoke("runtime-preflight.ps1", {"isolated": False, "secret": "do-not-log-this-secret"})
        self.assertFalse(result["ok"])
        self.assertEqual(result["scope"], "preflight_only")

    def test_preflight_missing_models_are_not_ready(self):
        result = self.invoke("runtime-preflight.ps1", {
            "version": 1, "isolated": True,
            "services": {"postgres": {"host": "127.0.0.1", "port": 1}},
            "models": {"embedding": {"pathEnv": "XDB_TEST_ABSENT_MODEL_20261106"}},
        })
        self.assertFalse(result["ok"])
        self.assertTrue(any(not item["ok"] for item in result["checks"]))

    def test_verify_required_cases_cannot_pass_with_no_implementation(self):
        result = self.invoke("verify-runtime.ps1", {"version": 1, "isolated": True}, "-Case", "All", "-RequireCases")
        self.assertFalse(result["ok"])
        self.assertIn("required_cases_missing", result["reason"])

    def test_preflight_success_is_only_connectivity_not_runtime_readiness(self):
        with socket.socket() as listener, tempfile.TemporaryDirectory() as model_path:
            listener.bind(("127.0.0.1", 0))
            listener.listen(8)
            endpoint = {"host": "127.0.0.1", "port": listener.getsockname()[1]}
            result = self.invoke("runtime-preflight.ps1", {
                "version": 1, "isolated": True,
                "services": {name: endpoint for name in ("postgres", "redis", "kafka", "milvus")},
                "models": {name: {"pathEnv": "XDB_TEST_MODEL_DIRECTORY"} for name in ("embedding", "reranker")},
            }, expect_success=True, extra_env={"XDB_TEST_MODEL_DIRECTORY": model_path})
        self.assertTrue(result["ok"])
        self.assertEqual(result["scope"], "preflight_only")
        self.assertEqual(len(result["checks"]), 8)
        self.assertIn("require_integration_tests", result["reason"])

    def test_verify_protocol_case_runs_the_fixture_tests(self):
        result = self.invoke("verify-runtime.ps1", {"version": 1, "isolated": True}, "-Case", "A01", "-RequireCases", expect_success=True)
        self.assertTrue(result["ok"])
        self.assertGreaterEqual(result["passedTests"], 43)
        self.assertFalse(result["servicesValidated"])


if __name__ == "__main__":
    unittest.main()
