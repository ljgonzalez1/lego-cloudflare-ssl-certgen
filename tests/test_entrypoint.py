"""Exercise argument processing and provider configuration through the entrypoint."""

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ENTRYPOINT = Path(__file__).resolve().parents[1] / "build" / "entrypoint.sh"
RECORD_ARGS = """#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

Path(os.environ['CERTGEN_TEST_ARGS']).write_text(json.dumps({
    'args': sys.argv[1:],
    'token': os.environ['CF_DNS_API_TOKEN'],
    'legacy_key_present': 'CLOUDFLARE_API_KEY' in os.environ,
}), encoding='utf-8')
"""


class EntrypointTests(unittest.TestCase):
    def setUp(self):
        workdir = tempfile.TemporaryDirectory()
        self.addCleanup(workdir.cleanup)
        root = Path(workdir.name)
        self.config = root / "external config.yaml"
        self.config.write_text(
            'first: [example.com, "*.app.example.com"]\nsecond: [example.org]\n', encoding="utf-8",
        )
        self.output = root / "certificate output"
        self.output.mkdir()
        self.arguments = root / "request.json"
        lego = root / "lego"
        lego.write_text(RECORD_ARGS, encoding="utf-8")
        lego.chmod(0o755)
        if os.geteuid() == 0:
            # Allow CLI tests on root hosts that lack Alpine's privilege helper.
            privilege_stub = root / "su-exec"
            privilege_stub.write_text('#!/bin/sh\nshift\nexec "$@"\n', encoding="utf-8")
            privilege_stub.chmod(0o755)
        self.env = {
            "PATH": os.pathsep.join((str(root), os.environ.get("PATH", os.defpath))),
            "CERT_UID": str(os.geteuid() or 1000),
            "CERT_GID": str(os.getegid() or 1000),
            "CERTGEN_TEST_ARGS": str(self.arguments),
        }

    def run_cli(self, *args, **overrides):
        return subprocess.run(
            ["bash", str(ENTRYPOINT), "--config", str(self.config), "--output", str(self.output), *args],
            env=self.env | overrides, capture_output=True, text=True, timeout=10,
        )

    def test_help_exits_before_credentials_or_identity_validation(self):
        result = self.run_cli("--help", CERT_UID="invalid")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Usage: certgen", result.stdout)

    def test_conflicting_action_and_mode_flags(self):
        for flags in (("--list", "--dry-run"), ("--production", "--staging")):
            with self.subTest(flags=flags):
                result = self.run_cli(*flags)
                self.assertEqual(result.returncode, 2)
                self.assertIn("mutually exclusive", result.stderr)

    def test_unknown_option_and_missing_option_values(self):
        unknown = self.run_cli("--unexpected")
        self.assertEqual(unknown.returncode, 2)
        self.assertIn("Unknown argument: --unexpected", unknown.stderr)
        for flag in ("--only", "--config", "--output"):
            with self.subTest(flag=flag):
                result = self.run_cli(flag)
                self.assertEqual(result.returncode, 2)
                self.assertIn(f"{flag} requires a value", result.stderr)

    def test_list_preserves_group_order_for_repeated_only(self):
        result = self.run_cli("--list", "--only", "second", "--only", "first", "--only", "first")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "first: 2 domains\nsecond: 1 domains\n")
        self.assertFalse(self.arguments.exists())

    def test_explicit_mode_overrides_environment_in_preview(self):
        for flag, default, server in (
            ("--staging", "true", "acme-staging-v02.api.letsencrypt.org"),
            ("--production", "false", "acme-v02.api.letsencrypt.org"),
        ):
            with self.subTest(flag=flag):
                result = self.run_cli("--dry-run", flag, "--only", "first", PRODUCTION=default)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f"https://{server}/directory", result.stdout)
                self.assertIn("*.app.example.com", result.stdout)
        self.assertFalse(self.arguments.exists())
        self.assertEqual(list(self.output.iterdir()), [])

    def test_invalid_ids_are_rejected_before_parsing_configuration(self):
        for key, value in (("CERT_UID", "0"), ("CERT_GID", "4294967295")):
            with self.subTest(key=key):
                result = self.run_cli("--list", **{key: value})
                self.assertEqual(result.returncode, 2)
                self.assertIn("must be an integer", result.stderr)

    @unittest.skipIf(os.geteuid() == 0, "checks the non-root identity guard")
    def test_non_root_identity_must_match_the_configuration(self):
        result = self.run_cli("--list", CERT_UID=str(os.geteuid() + 1))
        self.assertEqual(result.returncode, 1)
        self.assertIn("container user must match", result.stderr)

    def test_issuance_passes_cloudflare_token_dns_resolvers_and_wildcard(self):
        result = self.run_cli(
            "--production", "--only", "first", EMAIL="certs@example.com",
            ACCEPT_LEGO_TOS="true", CLOUDFLARE_API_TOKEN="stub-token",
            DNS_RESOLVERS="1.1.1.1:53, [2606:4700:4700::1111]:53",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        request = json.loads(self.arguments.read_text(encoding="utf-8"))
        args = request["args"]
        self.assertEqual(args[args.index("--dns") + 1], "cloudflare")
        self.assertEqual(args[args.index("--server") + 1], "https://acme-v02.api.letsencrypt.org/directory")
        self.assertEqual([args[i + 1] for i, arg in enumerate(args) if arg == "--dns.resolvers"], [
            "1.1.1.1:53", "[2606:4700:4700::1111]:53",
        ])
        self.assertEqual([args[i + 1] for i, arg in enumerate(args) if arg == "--domains"], [
            "example.com", "*.app.example.com",
        ])
        self.assertEqual(args[-1], "run")
        self.assertEqual(request["token"], "stub-token")
        self.assertFalse(request["legacy_key_present"])


if __name__ == "__main__":
    unittest.main()
