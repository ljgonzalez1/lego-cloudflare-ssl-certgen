"""Validate the public YAML reader with the same CLI used by the wrapper."""

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


READER = Path(__file__).resolve().parents[1] / "build" / "read_domains.py"


class DomainReaderTests(unittest.TestCase):
    def setUp(self):
        workdir = tempfile.TemporaryDirectory()
        self.addCleanup(workdir.cleanup)
        self.config = Path(workdir.name) / "domains with spaces.yaml"

    def read(self, source, *groups):
        if source is not None:
            self.config.write_text(source, encoding="utf-8")
        return subprocess.run(
            [sys.executable, str(READER), str(self.config), *groups],
            capture_output=True, text=True, timeout=10,
        )

    def assert_rejected(self, source, message, *groups):
        result = self.read(source, *groups)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertIn(message, result.stderr)

    def test_selected_groups_keep_yaml_order_and_literal_wildcards(self):
        result = self.read(
            'first:\n  - Example.com\n  - "*.lab.example.com"\n'
            'second:\n  - api.example.org\n'
            'third:\n  - example.net\n',
            "third", "first", "third",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "first\tExample.com,*.lab.example.com\nthird\texample.net\n")

    def test_missing_file_and_malformed_yaml_are_rejected(self):
        self.assert_rejected(None, "No such file")
        self.assert_rejected("first: [example.com\n", "Error in")

    def test_empty_or_wrong_yaml_shapes_are_rejected(self):
        for source, message in (
            ("", "expected a nonempty mapping"),
            ("- example.com\n", "expected a nonempty mapping"),
            ("first: []\n", "expected a nonempty domain list"),
            ("first: example.com\n", "expected a nonempty domain list"),
        ):
            with self.subTest(source=source):
                self.assert_rejected(source, message)

    def test_duplicate_or_invalid_folders_are_rejected(self):
        self.assert_rejected("first: [example.com]\nfirst: [example.org]\n", "duplicate certificate folder")
        for name in ("../escape", "with space", "_private", "x" * 101):
            with self.subTest(name=name):
                self.assert_rejected(f'"{name}": [example.com]\n', "invalid certificate folder")

    def test_unknown_selection_reports_distinct_missing_groups(self):
        self.assert_rejected(
            "first: [example.com]\n", "unknown certificate folder(s): missing, extra",
            "missing", "extra", "missing",
        )

    def test_duplicate_names_are_case_insensitive_within_each_group(self):
        self.assert_rejected("first: [Example.com, example.COM]\n", "duplicate DNS name")
        result = self.read("first: [example.com]\nsecond: [EXAMPLE.COM]\n")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_invalid_identifiers_are_rejected(self):
        invalid = (
            "true", '"192.0.2.1"', '"bad.123"', '"label.*.example.com"',
            '"*.*.example.com"', '"-bad.example.com"', '"bad_.example.com"',
            f'"{"a" * 64}.example.com"',
            '"' + ".".join(["a" * 63] * 4) + '"',
        )
        for value in invalid:
            with self.subTest(value=value):
                self.assert_rejected(f"first: [{value}]\n", "invalid DNS name")
        self.assert_rejected("first:\n  - *.example.com\n", "Error in")

    def test_one_hundred_unique_names_is_the_group_limit(self):
        names = [f"host{index}.example.com" for index in range(100)]
        source = "first:\n" + "".join(f"  - {name}\n" for name in names)
        result = self.read(source)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.rstrip().split("\t")[1].split(","), names)
        self.assert_rejected(source + "  - extra.example.com\n", "101 identifiers exceed the 100-name limit")

    def test_custom_python_yaml_tags_are_rejected(self):
        self.assert_rejected('first: !!python/object/apply:builtins.str [unsafe]\n', "Error in")


if __name__ == "__main__":
    unittest.main()
