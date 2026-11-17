"""Check platform selection and failure behavior with local release archives."""

import hashlib
import io
import os
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "build" / "download_lego.sh"
PLATFORMS = {
    ("amd64", ""): "linux_amd64",
    ("386", ""): "linux_386",
    ("arm", "v7"): "linux_armv7",
    ("arm64", ""): "linux_arm64",
    ("arm64", "v8"): "linux_arm64",
}
CURL_STUB = """#!/usr/bin/env python3
import os
import shutil
import sys
from pathlib import Path

asset = sys.argv[-1].rsplit('/', 1)[-1]
with Path(os.environ['CERTGEN_TEST_DOWNLOAD_LOG']).open('a', encoding='utf-8') as log:
    print(asset, file=log)
shutil.copyfile(Path(os.environ['CERTGEN_TEST_RELEASE_DIR']) / asset, sys.argv[sys.argv.index('-o') + 1])
"""


class ReleaseDownloadTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.log = self.root / "downloads.txt"
        release_dir = self.root / "release"
        release_dir.mkdir()
        checksums = []
        for asset_arch in sorted(set(PLATFORMS.values())):
            filename = f"lego_v4.34.0_{asset_arch}.tar.gz"
            archive = release_dir / filename
            with tarfile.open(archive, "w:gz") as handle:
                for name, content in (("lego", asset_arch.encode()), ("LICENSE", b"Fixture license")):
                    member = tarfile.TarInfo(name)
                    member.size = len(content)
                    handle.addfile(member, io.BytesIO(content))
            checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {filename}\n")
        self.checksums = release_dir / "lego_4.34.0_checksums.txt"
        self.checksums.write_text("".join(checksums), encoding="utf-8")
        curl = self.root / "curl"
        curl.write_text(CURL_STUB, encoding="utf-8")
        curl.chmod(0o755)
        self.env = {
            "PATH": os.pathsep.join((str(self.root), os.environ.get("PATH", os.defpath))),
            "LEGO_VERSION": "v4.34.0",
            "CERTGEN_TEST_DOWNLOAD_LOG": str(self.log),
            "CERTGEN_TEST_RELEASE_DIR": str(release_dir),
        }

    def download(self, arch, variant, output, **overrides):
        return subprocess.run(
            ["sh", str(SCRIPT), str(output)],
            env=self.env | {"TARGETARCH": arch, "TARGETVARIANT": variant} | overrides,
            capture_output=True, text=True, timeout=15,
        )

    def test_downloads_the_matching_asset_for_each_platform(self):
        for (arch, variant), asset_arch in PLATFORMS.items():
            with self.subTest(arch=arch, variant=variant):
                output = self.root / f"out-{arch}-{variant}"
                result = self.download(arch, variant, output)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual((output / "lego").read_text(), asset_arch)
                self.assertTrue(os.access(output / "lego", os.X_OK))
                self.assertEqual((output / "LICENSE").read_text(), "Fixture license")
                self.assertEqual(self.log.read_text().splitlines()[-2:], [
                    "lego_4.34.0_checksums.txt", f"lego_v4.34.0_{asset_arch}.tar.gz",
                ])

    def test_unsupported_platforms_fail_before_network_access(self):
        for arch, variant in (("arm", "v5"), ("arm", "v6"), ("arm", ""), ("ppc64le", "")):
            with self.subTest(arch=arch, variant=variant):
                result = self.download(arch, variant, self.root / "unsupported")
                self.assertEqual(result.returncode, 2)
                self.assertIn("Unsupported target platform", result.stderr)
                self.assertFalse(self.log.exists())

    def test_invalid_checksum_prevents_extraction(self):
        output = self.root / "unverified"
        result = self.download("amd64", "", output, LEGO_SHA256="0" * 64)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(output.exists())

    def test_missing_manifest_entry_stops_before_archive_download(self):
        self.checksums.write_text("", encoding="utf-8")
        output = self.root / "unlisted"
        result = self.download("arm", "v7", output)
        self.assertEqual(result.returncode, 1)
        self.assertIn("Missing or invalid SHA-256", result.stderr)
        self.assertEqual(self.log.read_text().splitlines(), [self.checksums.name])
        self.assertFalse(output.exists())

    def test_explicit_checksum_is_validated_before_any_download(self):
        for checksum, message in (("feedface", "Invalid SHA-256 length"), ("z" * 64, "Missing or invalid SHA-256")):
            with self.subTest(checksum=checksum):
                result = self.download("386", "", self.root / "invalid", LEGO_SHA256=checksum)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
                self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
