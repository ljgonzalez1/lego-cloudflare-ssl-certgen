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
        [cell.strip() for cell in line.split("|")[1:-1]]
        for line in plain.splitlines() if line.startswith("|")
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

    def test_dns01_submits_names_without_service_record_discovery(self):
        new_names = ["unpublished.example.test", "*.new.unpublished.example.test"]
        self.config.write_text(
            "planned:\n" + "".join(f'  - "{domain}"\n' for domain in new_names), encoding="utf-8",
        )
        for name in ("dig", "host", "nslookup", "getent", "curl", "wget"):
            resolver = self.output.parent / name
            resolver.write_text("#!/bin/sh\nprintf 'Unexpected preflight lookup\\n' >&2\nexit 1\n")
            resolver.chmod(0o755)
        result = self.run_certgen("planned")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("Unexpected preflight lookup", result.stderr)
        request, = self.requests()
        self.assertEqual(request["domains"], new_names)
        self.assertEqual(request["disable_cname"], "true")

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

    def test_invalid_token_skips_orders_without_printing_secret(self):
        invalid = "fake token with whitespace"
        result = self.run_certgen("mixed", CLOUDFLARE_API_KEY=invalid)
        self.assertEqual(result.returncode, 2)
        self.assertIn("Provide a valid Cloudflare DNS API token", result.stderr)
        self.assertNotIn(invalid, result.stdout + result.stderr)
        self.assertIn(["mixed", "0", "0", "0", "6", "SKIPPED"], summary_rows(result.stdout))
        self.assertFalse(self.log.exists())

    def test_invalid_settings_stop_issuance_before_any_acme_order(self):
        result = self.run_certgen(
            "mixed", DNS_RESOLVERS="1.1.1.1:65536", PROPAGATION_SECONDS="-1",
            PRODUCTION="invalid", EMAIL="invalid", ACCEPT_LEGO_TOS="false", TZ="../invalid",
        )
        self.assertEqual(result.returncode, 2)
        for field in ("DNS_RESOLVERS", "PROPAGATION_SECONDS", "PRODUCTION", "EMAIL", "ACCEPT_LEGO_TOS", "TZ"):
            self.assertIn(f"{field}:", result.stderr)
        self.assertFalse(self.log.exists())

    def test_missing_output_directory_reports_skipped_names(self):
        missing = self.output / "missing"
        result = self.run_certgen("mixed", CERTGEN_OUTPUT=str(missing))
        self.assertEqual(result.returncode, 1)
        self.assertIn("Output directory must be writable", result.stderr)
        self.assertIn(["mixed", "0", "0", "0", "6", "SKIPPED"], summary_rows(result.stdout))
        self.assertFalse(self.log.exists())

    def test_same_second_retries_use_separate_certificate_directories(self):
        clock = self.output.parent / "date"
        date = shlex.quote(shutil.which("date"))
        clock.write_text(
            '#!/bin/sh\nif [ "$1" = +%s ]; then printf "1700000000\\n"; exit; fi\n'
            f'exec {date} "$@"\n', encoding="utf-8",
        )
        clock.chmod(0o755)
        for _ in range(2):
            result = self.run_certgen("mixed", TZ="Etc/UTC")
            self.assertEqual(result.returncode, 0, result.stderr)
        stamps = sorted(path.name for path in (self.output / "mixed").iterdir())
        self.assertEqual(stamps, ["2023-11-14_22.13.20_GMT+0", "2023-11-14_22.13.21_GMT+0"])
        self.assertEqual(len(self.requests()), 2)

    @unittest.skipUnless(shutil.which("script"), "requires util-linux script for a pseudo-terminal")
    def test_summary_colors_terminal_output(self):
        result = self.run_certgen("mixed", tty=True)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("\x1b[32mISSUED", result.stdout)
        self.assertIn(["mixed", "6", "6", "0", "0", "ISSUED"], summary_rows(result.stdout))

    @unittest.skipUnless(shutil.which("script"), "requires util-linux script for a pseudo-terminal")
    def test_summary_aligns_in_narrow_terminals(self):
        long_name = "x" * 100
        self.config.write_text(f"{long_name}:\n  - alias.example.test\n", encoding="utf-8")
        for columns in (80, 48, 28):
            with self.subTest(columns=columns):
                result = self.run_certgen(tty=True, COLUMNS=str(columns))
                self.assertEqual(result.returncode, 0, result.stdout)
                plain = re.sub(r"\x1b\[[0-9;]*m", "", result.stdout)
                grid = [line for line in plain.splitlines() if line.startswith(("|", "+"))]
                self.assertTrue(grid)
                self.assertEqual(len({len(line) for line in grid}), 1)
                self.assertLessEqual(len(grid[0]), columns)
                self.assertEqual(sum(line.count("x") for line in grid), len(long_name))


if __name__ == "__main__":
    unittest.main()
