#!/usr/bin/env python3
"""Test notice installation and rejection of damaged or non-text-only exports."""

import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from license_bundle import install, validate


class LicenseBundleTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.work = Path(directory.name)
        self.export = self.work / "export"
        (self.export / "texts").mkdir(parents=True)
        notice = b"Copyright fixture authors.\nPermission is hereby granted, free of charge.\n"
        self.digest = hashlib.sha256(notice).hexdigest()
        self.notice = self.export / "texts" / (self.digest + ".txt")
        self.notice.write_bytes(notice)
        component_id = "module:example.test/fixture@v1.0.0"
        self.bundle = {
            "schema": 1,
            "components": [{"id": component_id, "kind": "go-module", "name": "example.test/fixture",
                            "version": "v1.0.0", "origin": "offline fixture", "source_sum": "h1:fixture",
                            "documents": [{"source": "LICENSE", "sha256": self.digest,
                                           "path": "usr/share/licenses/components/fixture/LICENSE"}]}],
            "data_files": [{"path": "fixture", "sha256": self.digest, "component": component_id}],
        }
        self.write_bundle()
        self.destination = self.work / "project/build/licenses/third-party"

    def write_bundle(self):
        (self.export / "bundle.json").write_text(json.dumps(self.bundle))

    def test_installs_original_texts_and_preserves_readme(self):
        self.destination.mkdir(parents=True)
        (self.destination / "README.md").write_text("Upstream attribution instructions\n")
        (self.destination / "stale.txt").write_text("stale notice")
        self.assertEqual(install(self.export, self.destination), 1)
        self.assertEqual((self.destination / "texts" / self.notice.name).read_bytes(), self.notice.read_bytes())
        self.assertTrue((self.destination / "README.md").exists())
        self.assertFalse((self.destination / "stale.txt").exists())

    def test_changed_notice_keeps_previous_bundle(self):
        self.destination.mkdir(parents=True)
        old = self.destination / "previous.txt"
        old.write_text("previous reviewed evidence")
        self.notice.write_text("changed notice")
        with self.assertRaisesRegex(ValueError, "checksum changed"):
            install(self.export, self.destination)
        self.assertEqual(old.read_text(), "previous reviewed evidence")

    def test_executables_and_extra_files_are_not_imported(self):
        for name in ("lego", "archive.zip", "extra.txt", "texts/unreferenced.txt"):
            with self.subTest(name=name):
                extra = self.export / name
                extra.write_bytes(b"not an exported notice")
                with self.assertRaisesRegex(ValueError, "unexpected file"):
                    validate(self.export)
                extra.unlink()
        self.notice.chmod(0o755)
        with self.assertRaisesRegex(ValueError, "executable"):
            validate(self.export)

    def test_symlinks_are_not_imported(self):
        self.notice.unlink()
        self.notice.symlink_to(self.work / "outside.txt")
        with self.assertRaises(ValueError):
            validate(self.export)

    def test_missing_export_and_duplicate_json_are_rejected(self):
        (self.export / "bundle.json").unlink()
        with self.assertRaisesRegex(ValueError, "missing"):
            validate(self.export)
        (self.export / "bundle.json").write_text('{"schema":1,"schema":1}')
        with self.assertRaisesRegex(ValueError, "duplicate JSON"):
            validate(self.export)

    def test_unsafe_notice_name_is_rejected(self):
        self.bundle["components"][0]["documents"][0]["source"] = "../LICENSE"
        self.write_bundle()
        with self.assertRaisesRegex(ValueError, "notice path"):
            validate(self.export)

    def test_duplicate_component_and_incomplete_bundle_are_rejected(self):
        self.bundle["components"].append(self.bundle["components"][0])
        self.write_bundle()
        with self.assertRaisesRegex(ValueError, "duplicate"):
            validate(self.export)
        self.bundle["components"] = []
        self.write_bundle()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            validate(self.export)


if __name__ == "__main__":
    unittest.main(verbosity=2)
