#!/usr/bin/env python3
"""Offline tests of real debug Go executables, including all five ELF targets.

Run from the repository root with Go installed: python3 -B ci/test_go_binary.py
Set GO to an absolute Go compiler path when it is not in PATH. No module is
downloaded: application dependencies are vendored. No ACME order is submitted.
"""

from __future__ import annotations

import os
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / "build"
GO = os.environ.get("GO", "go")
FLAGS = ["-buildvcs=false", "-mod=vendor", "-gcflags=all=-N -l -d=checkptr=2", "-ldflags=-compressdwarf=false"]
FAKE_LEGO = r'''package main
import("fmt"; "os"; "path/filepath"; "strings")
func main(){
 if len(os.Args)==2 && os.Args[1]=="--version" {fmt.Println("lego version 4.34.0 linux/fixture"); return}
 path, name, server := "", "", ""
 for i:=1;i+1<len(os.Args);i++ {switch os.Args[i] {case "--path":path=os.Args[i+1];case "--server":server=os.Args[i+1];case "--domains":if name=="" {name=os.Args[i+1]}}}
 if path=="" || name=="" {panic("invalid fixture invocation")}
 if strings.HasPrefix(name,"*."){name="_"+name[1:]}
 if err:=os.MkdirAll(filepath.Join(path,"certificates"),0700);err!=nil{panic(err)}
 for _,ext:=range []string{".crt",".key"} {if err:=os.WriteFile(filepath.Join(path,"certificates",name+ext),[]byte("offline fixture"),0600);err!=nil{panic(err)}}
 fmt.Println("OFFLINE FIXTURE SERVER="+server)
}
'''


def compile_go(output: Path, package: str, **settings: str) -> None:
    environment = {**os.environ, "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "", **settings}
    result = subprocess.run([GO, "build", *FLAGS, "-o", str(output), package], cwd=BUILD, env=environment, capture_output=True, text=True, timeout=300)
    if result.returncode:
        raise RuntimeError(f"Go build failed for {package}:\n{result.stdout}{result.stderr}")


class CompiledGoTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.temporary = tempfile.TemporaryDirectory(prefix="certgen-go-binary-")
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.workspace = Path(cls.temporary.name)
        cls.workspace.chmod(0o755)
        cls.binary = cls.workspace / "certgen"
        cls.builder = cls.workspace / "certgen-build"
        compile_go(cls.binary, "./cmd/certgen")
        compile_go(cls.builder, "./cmd/certgen-build")
        cls.fakebin = cls.workspace / "bin"
        cls.fakebin.mkdir(mode=0o755)
        fixture = cls.workspace / "lego.go"
        fixture.write_text(FAKE_LEGO, encoding="utf-8")
        compile_go(cls.fakebin / "lego", str(fixture))

    def setUp(self) -> None:
        self.work = self.workspace / self.id().rsplit(".", 1)[-1]
        self.work.mkdir(mode=0o777)
        self.work.chmod(0o777)  # Also exercise root -> UID 1000 when run in a container.
        self.config = self.work / "domains.yaml"
        self.config.write_text("first: [example.com, www.example.com, '*.example.org']\n", encoding="utf-8")
        self.config.chmod(0o644)
        self.output = self.work / "certificates"
        self.environment = {key: value for key, value in os.environ.items() if not key.startswith(("CF_", "CLOUDFLARE_", "CERT_"))}
        self.environment.update(PATH=str(self.fakebin), UID=str(os.getuid() or 1000), GID=str(os.getgid() or 1000), TZ="Etc/UTC", EMAIL="ci@example.com", ACCEPT_LEGO_TOS="true", CLOUDFLARE_API_TOKEN="test-private-token", LEGO_OUTPUT="raw", CERTGEN_TRACE="errors")

    def launch(self, *arguments: str, expected: int = 0, **environment: str) -> subprocess.CompletedProcess[str]:
        result = subprocess.run([str(self.binary), "--config", str(self.config), "--output", str(self.output), *arguments], env={**self.environment, **environment}, input="", capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        self.assertNotIn("test-private-token", result.stdout + result.stderr)
        return result

    def test_debug_sections_and_build_settings(self) -> None:
        result = subprocess.run([str(self.builder), "verify-debug", str(self.binary)], capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        result = subprocess.run([GO, "version", "-m", str(self.binary)], capture_output=True, text=True, check=True)
        for flag in ("-N -l -d=checkptr=2", "-compressdwarf=false", "CGO_ENABLED=0", "gopkg.in/yaml.v3"):
            self.assertIn(flag, result.stdout)

    def test_debug_checker_rejects_a_disabled_no_optimization_flag(self) -> None:
        # A substring match for '-N' would wrongly accept '-N=false'. Keep this
        # check against a real compiled binary instead of fabricated metadata.
        target = self.work / "incorrect-debug-flags"
        result = subprocess.run([GO, "build", "-buildvcs=false", "-mod=vendor",
                                 "-gcflags=all=-N=false -l -d=checkptr=2", "-ldflags=-compressdwarf=false",
                                 "-o", str(target), "./cmd/certgen"], cwd=BUILD,
                                env={**os.environ, "CGO_ENABLED": "0", "GOWORK": "off", "GOFLAGS": ""},
                                capture_output=True, text=True, timeout=300)
        self.assertEqual(result.returncode, 0, result.stderr)
        check = subprocess.run([str(self.builder), "verify-debug", str(target)], capture_output=True, text=True, timeout=15)
        self.assertEqual(check.returncode, 1, check.stderr)
        self.assertIn("debug compiler settings", check.stderr)

    def test_list_and_preview_make_no_files_or_orders(self) -> None:
        self.assertIn("first: 3 domains", self.launch("--list", PATH="").stdout)
        self.assertIn("acme-staging", self.launch("--dry-run", PATH="").stdout)
        self.assertFalse(self.output.exists())

    def test_testing_mode_is_unattended_and_offline(self) -> None:
        result = self.launch("--test", EMAIL="", CLOUDFLARE_API_TOKEN="", ACCEPT_LEGO_TOS="false")
        self.assertIn("TEST MODE (offline)", result.stdout)
        self.assertIn("Offline test passed.", result.stdout)
        self.assertIn("acme-staging", result.stdout)
        self.assertEqual(result.stderr, "")
        self.assertFalse(self.output.exists())

    def test_no_flag_needs_a_terminal(self) -> None:
        result = self.launch(expected=2)
        self.assertIn("terminal", result.stderr.lower())
        self.assertFalse(self.output.exists())

    def test_explicit_staging_and_production_use_correct_servers(self) -> None:
        self.output.mkdir(mode=0o777)
        self.output.chmod(0o777)
        for flag, server in (("--staging", "acme-staging-v02.api.letsencrypt.org"), ("--production", "acme-v02.api.letsencrypt.org")):
            with self.subTest(flag=flag):
                result = self.launch(flag)
                self.assertIn("OFFLINE FIXTURE SERVER=https://" + server, result.stderr)
                self.assertIn("ISSUED", result.stdout)
                self.assertRegex(result.stdout, r"\| first\s*\|\s*3\s*\|\s*3\s*\|\s*0\s*\|\s*0\s*\| ISSUED")
                self.assertIn("Certificates: 1 attempted, 1 issued, 0 failed, 0 skipped.", result.stdout)
        self.assertEqual(len(list(self.output.rglob("*.crt"))), 2)
        self.assertEqual(len(list(self.output.rglob("*.key"))), 2)

    def test_conflicting_or_unknown_flags_fail_with_beta_trace(self) -> None:
        for arguments in (("--test", "--production"), ("--production", "--staging"), ("--typo",)):
            with self.subTest(arguments=arguments):
                result = self.launch(*arguments, expected=2)
                self.assertIn("[alpha trace]", result.stderr)
                self.assertIn("goroutine ", result.stderr)
                self.assertFalse(self.output.exists())

    def test_invalid_yaml_shows_a_trace_and_no_files(self) -> None:
        self.config.write_text("first: [invalid]\n", encoding="utf-8")
        result = self.launch("--dry-run", expected=2)
        self.assertIn("invalid DNS name", result.stderr)
        self.assertIn("[alpha trace]", result.stderr)
        self.assertFalse(self.output.exists())

    def test_successful_runs_keep_diagnostics_hidden(self) -> None:
        result = self.launch("--dry-run")
        self.assertEqual(result.stderr, "")
        self.assertNotIn("[alpha trace]", result.stdout)

    def test_trace_can_be_requested_or_disabled(self) -> None:
        self.assertIn("[alpha trace]", self.launch("--dry-run", CERTGEN_TRACE="always").stderr)
        self.assertNotIn("[alpha trace]", self.launch("--typo", expected=2, CERTGEN_TRACE="off").stderr)

    def test_missing_lego_fails_without_hanging(self) -> None:
        result = self.launch("--test", expected=1, PATH="")
        self.assertIn("Could not check the lego executable", result.stderr)
        self.assertFalse(self.output.exists())

    def test_removed_production_variable_cannot_change_preview(self) -> None:
        result = self.launch("--dry-run", PRODUCTION="true", STAGING="false")
        self.assertIn("acme-staging", result.stdout)
        self.assertNotIn("PRODUCTION MODE", result.stdout)

    def test_all_five_targets_have_real_debug_elf_binaries(self) -> None:
        for architecture, level, machine in (("amd64", "", 62), ("386", "", 3), ("arm", "6", 40), ("arm", "7", 40), ("arm64", "", 183)):
            with self.subTest(architecture=architecture, level=level):
                target = self.work / ("certgen-" + architecture + level)
                compile_go(target, "./cmd/certgen", GOOS="linux", GOARCH=architecture, GOARM=level, GOAMD64="v1")
                with target.open("rb") as stream:
                    header = stream.read(20)
                self.assertEqual(header[:4], b"\x7fELF")
                self.assertEqual(struct.unpack_from("<H", header, 18)[0], machine)
                check = subprocess.run([str(self.builder), "verify-debug", str(target)], capture_output=True, text=True, timeout=30)
                self.assertEqual(check.returncode, 0, check.stderr)
                info = subprocess.run([GO, "version", "-m", str(target)], check=True, capture_output=True, text=True).stdout
                self.assertIn("GOARCH=" + architecture, info)
                self.assertIn("CGO_ENABLED=0", info)
                if level:
                    self.assertIn("GOARM=" + level, info)


if __name__ == "__main__":
    unittest.main(verbosity=2)
