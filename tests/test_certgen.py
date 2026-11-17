"""Exercise the Bash wrapper with a local lego stub and no DNS or ACME calls."""

import json
import os
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "build" / "run_lego.sh"
MIXED_NAMES = [
    "ipv4.example.test", "*.ipv4.example.test",
    "ipv6.example.test", "*.ipv6.example.test",
    "alias.example.test", "*.alias.example.test",
]
SKIPPED_GROUP = "skipped_certificate_with_a_long_name"
STUB = """#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
output = Path(args[args.index('--path') + 1])
group = output.parents[1].name
with open(os.environ['CERTGEN_TEST_LOG'], 'a', encoding='utf-8') as log:
    print(json.dumps({
        'group': group,
        'domains': [args[index + 1] for index, arg in enumerate(args) if arg == '--domains'],
        'disable_cname': os.environ['LEGO_DISABLE_CNAME_SUPPORT'],
        'propagation_seconds': os.environ['CLOUDFLARE_PROPAGATION_TIMEOUT'],
    }), file=log)
sys.exit(7 if group == 'rejected' else 0)
"""


def summary_rows(output):
    plain = re.sub(r"\x1b\[[0-9;]*m", "", output)
    return [
        [cell.strip() for cell in line.split("│")[1:-1]]
        for line in plain.splitlines() if line.startswith("│")
    ]


class CertgenTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        workdir = Path(temp.name)
        self.log = workdir / "lego.jsonl"
        self.output = workdir / "output"
        self.output.mkdir()
        self.config = workdir / "domains.yaml"
        self.config.write_text(
            "mixed:\n" + "".join(f"  - '{domain}'\n" for domain in MIXED_NAMES)
            + "rejected:\n  - 'rejected.example.test'\n  - '*.rejected.example.test'\n"
            f"{SKIPPED_GROUP}:\n  - 'skipped.example.test'\n"
            "last:\n  - 'last.example.test'\n",
            encoding="utf-8",
        )
        lego = workdir / "lego"
        lego.write_text(STUB, encoding="utf-8")
        lego.chmod(0o755)
        self.env = {
            "PATH": os.pathsep.join((str(workdir), os.environ.get("PATH", os.defpath))),
            "LANG": "C.UTF-8",
            "EMAIL": "certs@example.test",
            "CLOUDFLARE_API_KEY": "test-token",
            "ACCEPT_LEGO_TOS": "true",
            "CERT_UID": "1000",
            "CERT_GID": "1000",
            "CERTGEN_ACTION": "issue",
            "CERTGEN_CONFIG": str(self.config),
            "CERTGEN_OUTPUT": str(self.output),
            "CERTGEN_TEST_LOG": str(self.log),
        }

    def run_certgen(self, *groups, tty=False, **overrides):
        command = ["bash", str(SCRIPT), *groups]
        if tty:
            command = ["script", "-q", "-e", "-c", shlex.join(command), os.devnull]
        return subprocess.run(
            command, env=self.env | overrides, capture_output=True, text=True,
            stdin=subprocess.DEVNULL, timeout=15,
        )

    def requests(self):
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]

    def test_default_uses_direct_txt_for_ordinary_names_and_wildcards(self):
        result = self.run_certgen("mixed")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.requests(), [{
            "group": "mixed", "domains": MIXED_NAMES,
            "disable_cname": "true", "propagation_seconds": "360",
        }])
        self.assertIn(["mixed", "6", "6", "0", "0", "ISSUED"], summary_rows(result.stdout))
        self.assertNotIn("\x1b[", result.stdout)

    def test_explicit_cname_following_and_timeout_override(self):
        result = self.run_certgen("mixed", LEGO_DISABLE_CNAME_SUPPORT="false", PROPAGATION_SECONDS="90")
        self.assertEqual(result.returncode, 0, result.stderr)
        request, = self.requests()
        self.assertEqual(request["disable_cname"], "false")
        self.assertEqual(request["propagation_seconds"], "90")

    def test_summary_keeps_yaml_order_and_counts_failed_and_skipped_groups(self):
        (self.output / SKIPPED_GROUP).symlink_to(self.config)
        result = self.run_certgen()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("rejected: lego exited with code 7", result.stderr)
        self.assertEqual([request["group"] for request in self.requests()], ["mixed", "rejected", "last"])
        self.assertEqual(summary_rows(result.stdout), [
            ["Certificate", "Attempted", "Issued", "Failed", "Skipped", "Status"],
            ["mixed", "6", "6", "0", "0", "ISSUED"],
            ["rejected", "2", "0", "2", "0", "FAILED"],
            [SKIPPED_GROUP, "0", "0", "0", "1", "SKIPPED"],
            ["last", "1", "1", "0", "0", "ISSUED"],
            ["TOTAL", "9", "7", "2", "1", ""],
        ])
        self.assertIn("3 attempted, 2 issued, 1 failed, 1 skipped", result.stdout)

    def test_invalid_cname_setting_reports_groups_skipped_before_lego(self):
        result = self.run_certgen("mixed", LEGO_DISABLE_CNAME_SUPPORT="invalid")
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("LEGO_DISABLE_CNAME_SUPPORT must be true or false", result.stderr)
        self.assertIn(["mixed", "0", "0", "0", "6", "SKIPPED"], summary_rows(result.stdout))
        self.assertFalse(self.log.exists())

    def test_list_and_dry_run_do_not_submit_orders(self):
        result = self.run_certgen("mixed", CERTGEN_ACTION="list", CLOUDFLARE_API_KEY="")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "mixed: 6 domains\n")
        result = self.run_certgen("mixed", CERTGEN_ACTION="dry-run", CLOUDFLARE_API_KEY="")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("*.alias.example.test", result.stdout)
        self.assertFalse(self.log.exists())
        self.assertEqual(list(self.output.iterdir()), [])

    @unittest.skipUnless(shutil.which("script"), "requires util-linux script for a pseudo-terminal")
    def test_summary_colors_terminal_output(self):
        result = self.run_certgen("mixed", tty=True)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("\x1b[32mISSUED", result.stdout)
        self.assertIn(["mixed", "6", "6", "0", "0", "ISSUED"], summary_rows(result.stdout))


if __name__ == "__main__":
    unittest.main()
