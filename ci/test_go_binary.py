#!/usr/bin/env python3
"""Offline tests of real debug Go executables, including all five ELF targets.

Run from the repository root with Go installed: python3 -B ci/test_go_binary.py
Set GO to an absolute Go compiler path when it is not in PATH. No module is
downloaded: application dependencies are vendored. No ACME order is submitted.
"""

from __future__ import annotations

import os
import select
import signal
from pathlib import Path
import struct
import re
import platform
import pty
import termios
import subprocess
import tempfile
import unittest
import time

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / "build"
GO = os.environ.get("GO", "go")
FLAGS = ["-buildvcs=false", "-mod=vendor", "-gcflags=all=-N -l -d=checkptr=2", "-ldflags=-compressdwarf=false"]
FAKE_LEGO = r'''package main
import("fmt"; "os"; "path/filepath"; "strings"; "time")
func main(){
 if len(os.Args)==2 && os.Args[1]=="--version" {fmt.Println("lego version 4.34.0 linux/fixture"); return}
 path, name, server := "", "", ""
 for i:=1;i+1<len(os.Args);i++ {switch os.Args[i] {case "--path":path=os.Args[i+1];case "--server":server=os.Args[i+1];case "--domains":if name=="" {name=os.Args[i+1]}}}
 if path=="" || name=="" {panic("invalid fixture invocation")}
 if os.Getenv("CERTGEN_FIXTURE_DELAY")!="" {
  fmt.Fprintf(os.Stderr,"%s [INFO] [%s] acme: Trying to solve DNS-01\n",time.Now().Format("2006/01/02 15:04:05"),name)
  fmt.Fprintf(os.Stderr,"%s [INFO] Wait for propagation [timeout: 1m0s, interval: 2s]\n",time.Now().Format("2006/01/02 15:04:05"))
  if os.Getenv("CERTGEN_FIXTURE_DELAY")=="long" {time.Sleep(10*time.Second)} else {time.Sleep(900*time.Millisecond)}
  fmt.Fprintf(os.Stderr,"%s [INFO] [%s] The server validated our request\n",time.Now().Format("2006/01/02 15:04:05"),name)
 }
 if strings.HasPrefix(name,"*."){name="_"+name[1:]}
 if err:=os.MkdirAll(filepath.Join(path,"certificates"),0700);err!=nil{panic(err)}
 for _,ext:=range []string{".crt",".key"} {if err:=os.WriteFile(filepath.Join(path,"certificates",name+ext),[]byte("offline fixture"),0600);err!=nil{panic(err)}}
 fmt.Println("OFFLINE FIXTURE SERVER="+server)
}
'''


def compile_go(output: Path, package: str, *, application_version: str = "", **settings: str) -> None:
    environment = {**os.environ, "CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "", **settings}
    flags = list(FLAGS)
    if application_version:
        flags[-1] += " -X=main.applicationVersion=" + application_version
    result = subprocess.run([GO, "build", *flags, "-o", str(output), package], cwd=BUILD, env=environment, capture_output=True, text=True, timeout=300)
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
        cls.crash_fixture = cls.workspace / "crash-worker"
        compile_go(cls.crash_fixture, "./internal/debugrun/testdata/worker")

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
        self.environment["CERTGEN_LOG_DIR"] = str(self.work / "debug-logs")

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
                self.assertIn("[debug trace]", result.stderr)
                self.assertIn("goroutine ", result.stderr)
                self.assertFalse(self.output.exists())

    def test_invalid_yaml_shows_a_trace_and_no_files(self) -> None:
        self.config.write_text("first: [invalid]\n", encoding="utf-8")
        result = self.launch("--dry-run", expected=2)
        self.assertIn("invalid DNS name", result.stderr)
        self.assertIn("[debug trace]", result.stderr)
        self.assertFalse(self.output.exists())

    def test_successful_runs_keep_diagnostics_hidden(self) -> None:
        result = self.launch("--dry-run")
        self.assertEqual(result.stderr, "")
        self.assertNotIn("[debug trace]", result.stdout)

    def test_trace_can_be_requested_or_disabled(self) -> None:
        self.assertIn("[debug trace]", self.launch("--dry-run", CERTGEN_TRACE="always").stderr)
        self.assertNotIn("[debug trace]", self.launch("--typo", expected=2, CERTGEN_TRACE="off").stderr)

    def test_success_always_writes_a_private_versioned_log(self) -> None:
        result = self.launch("--test")
        directory = Path(self.environment["CERTGEN_LOG_DIR"])
        files = list(directory.glob("*.log"))
        self.assertEqual(len(files), 1)
        architecture = {"x86_64":"amd64", "aarch64":"arm64", "arm64":"arm64", "i386":"i386", "i686":"i386", "armv6l":"armv6", "armv7l":"armv7"}[platform.machine()]
        self.assertRegex(files[0].name, r"^v6\.0\.1-beta-6\.\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}\."+architecture+r"\.log$")
        self.assertEqual(files[0].stat().st_mode & 0o777, 0o600)
        contents = files[0].read_text()
        self.assertIn("Offline test passed.", contents)
        self.assertIn("target final snapshot", contents)
        self.assertIn("goroutine ", contents)
        self.assertIn("exit status 0", contents)
        self.assertNotIn("test-private-token", contents)
        self.assertEqual(result.stderr, "")

    def test_failure_writes_a_dump_even_with_console_traces_disabled(self) -> None:
        self.launch("--typo", expected=2, CERTGEN_TRACE="off")
        files = list(Path(self.environment["CERTGEN_LOG_DIR"]).glob("*.log"))
        self.assertEqual(len(files), 2)
        normal = next(path for path in files if not path.name.startswith("dump."))
        dump = normal.with_name("dump." + normal.name)
        self.assertTrue(dump.exists())
        self.assertIn("Unknown argument", dump.read_text())
        self.assertIn("goroutine ", dump.read_text())
        self.assertEqual(dump.stat().st_mode & 0o777, 0o600)

    def test_linked_release_version_controls_log_filenames(self) -> None:
        version = "6.0.1-beta-6.build.7"
        binary = self.work / "linked-version"
        compile_go(binary, "./cmd/certgen", application_version=version)
        result = subprocess.run([str(binary), "--help"], env=self.environment,
                                capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        logs = list(Path(self.environment["CERTGEN_LOG_DIR"]).glob("*.log"))
        self.assertEqual(len(logs), 1)
        self.assertTrue(logs[0].name.startswith("v" + version + "."), logs[0].name)
        self.assertIn("exit status 0", logs[0].read_text())

    def test_concurrent_offline_processes_keep_separate_complete_logs(self) -> None:
        arguments = [str(self.binary), "--config", str(self.config), "--output", str(self.output), "--test"]
        processes = [subprocess.Popen(arguments, env=self.environment, stdin=subprocess.DEVNULL,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(4)]
        try:
            for process in processes:
                stdout, stderr = process.communicate(timeout=15)
                self.assertEqual(process.returncode, 0, stdout + stderr)
                self.assertIn(b"Offline test passed.", stdout)
            logs = list(Path(self.environment["CERTGEN_LOG_DIR"]).glob("*.log"))
            self.assertEqual(len(logs), 4)
            self.assertFalse(self.output.exists())
            for log in logs:
                self.assertFalse(log.name.startswith("dump."))
                self.assertEqual(log.stat().st_mode & 0o777, 0o600)
                self.assertIn("Offline test passed.", log.read_text())
                self.assertIn("target final snapshot", log.read_text())
                self.assertIn("exit status 0", log.read_text())
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)

    def test_pretty_console_keeps_full_lego_output_in_the_log(self) -> None:
        self.output.mkdir(mode=0o777)
        self.output.chmod(0o777)
        result = self.launch("--staging", LEGO_OUTPUT="pretty")
        normal = next(Path(self.environment["CERTGEN_LOG_DIR"]).glob("v*.log"))
        self.assertIn("OFFLINE FIXTURE SERVER=https://acme-staging", normal.read_text())
        self.assertNotIn("OFFLINE FIXTURE SERVER=", result.stdout)

    def test_invalid_temporary_directory_does_not_break_help(self) -> None:
        result = self.launch("--help", TMPDIR=str(self.work / "absent-temp-directory"))
        self.assertIn("Usage: certgen", result.stdout)
        self.assertTrue(list(Path(self.environment["CERTGEN_LOG_DIR"]).glob("v*.log")))

    def test_failure_of_both_log_locations_does_not_break_help(self) -> None:
        forbidden = self.work / "not-a-directory"
        forbidden.write_text("ordinary file")
        result = self.launch("--help", CERTGEN_LOG_DIR=str(forbidden), TMPDIR=str(self.work / "absent-temp-directory"))
        self.assertIn("Usage: certgen", result.stdout)
        self.assertIn("persistent diagnostics are unavailable", result.stderr)

    def test_unwritable_logging_does_not_break_the_application(self) -> None:
        forbidden = self.work / "not-a-directory"
        forbidden.write_text("ordinary file")
        result = self.launch("--help", CERTGEN_LOG_DIR=str(forbidden))
        self.assertIn("Usage: certgen", result.stdout)
        self.assertIn("Logs:", result.stderr)
        fallback = re.search(r"Logs: (.+\.log)", result.stderr)
        self.assertIsNotNone(fallback)
        if fallback:
            path = Path(fallback[1])
            self.addCleanup(lambda: __import__("shutil").rmtree(path.parent, ignore_errors=True))
            self.assertTrue(path.exists())

    def test_real_unrecovered_panic_and_abrupt_exit_leave_dumps(self) -> None:
        for mode, expected in (("panic", 2), ("exit", 7), ("kill", 137), ("descendant", 8)):
            with self.subTest(mode=mode):
                logs = self.work / mode
                descendant = self.work / (mode + "-pid")
                environment = self.environment | {"CERTGEN_LOG_DIR": str(logs), "CERTGEN_FIXTURE_MODE": mode,
                                                   "CERTGEN_FIXTURE_DESCENDANT": str(descendant)}
                result = subprocess.run([str(self.crash_fixture)], env=environment, input="", text=True,
                                        capture_output=True, timeout=15)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                normal = next(path for path in logs.glob("v*.log"))
                dump = logs / ("dump." + normal.name)
                self.assertTrue(dump.exists())
                contents = dump.read_text()
                self.assertIn("fixture stdout", contents)
                self.assertIn("fixture stderr", contents)
                self.assertIn("goroutine ", contents)
                self.assertNotIn("test-private-token", contents)
                if mode == "panic":
                    self.assertIn("unrecovered fixture panic", contents)
                if descendant.exists():
                    state = Path("/proc") / descendant.read_text().strip() / "stat"
                    self.assertTrue(not state.exists() or state.read_text().split()[2] == "Z", "orphaned child remained running")

    def test_crash_restores_a_terminal_left_in_raw_mode(self) -> None:
        master, slave = pty.openpty()
        try:
            before = termios.tcgetattr(slave)
            environment = self.environment | {"CERTGEN_FIXTURE_MODE":"panic", "CERTGEN_FIXTURE_RAW":"1"}
            process = subprocess.Popen([str(self.crash_fixture)], env=environment, stdin=slave,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            stdout, stderr = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 2, stdout + stderr)
            self.assertEqual(termios.tcgetattr(slave), before)
            self.assertTrue(list(Path(self.environment["CERTGEN_LOG_DIR"]).glob("dump.*.log")))
        finally:
            os.close(slave)
            os.close(master)

    def test_missing_lego_fails_without_hanging(self) -> None:
        result = self.launch("--test", expected=1, PATH="")
        self.assertIn("Could not check the lego executable", result.stderr)
        self.assertFalse(self.output.exists())

    def test_removed_production_variable_cannot_change_preview(self) -> None:
        result = self.launch("--dry-run", PRODUCTION="true", STAGING="false")
        self.assertIn("acme-staging", result.stdout)
        self.assertNotIn("PRODUCTION MODE", result.stdout)

    def terminal_issuance(self, *, interrupt=False, **settings):
        self.output.mkdir(mode=0o777, exist_ok=True)
        self.output.chmod(0o777)
        master, slave = pty.openpty()
        original = termios.tcgetattr(slave)
        environment = self.environment | {"TERM":"xterm", "COLUMNS":"100", "LINES":"30",
                                          "LEGO_OUTPUT":"pretty", "CERTGEN_FIXTURE_DELAY":"long" if interrupt else "1"}
        environment.pop("NO_COLOR", None)
        environment.update(settings)
        process = subprocess.Popen([str(self.binary), "--config", str(self.config), "--output", str(self.output), "--staging"],
                                   stdin=subprocess.DEVNULL, stdout=slave, stderr=slave, env=environment)
        collected = bytearray()
        deadline = time.monotonic() + 15
        sent_signal = False
        try:
            while True:
                if time.monotonic() >= deadline:
                    self.fail("terminal issuance timed out")
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    collected.extend(os.read(master, 65536))
                if interrupt and not sent_signal and "⢾⣿⡷".encode() in collected:
                    process.send_signal(signal.SIGTERM)
                    sent_signal = True
                if process.poll() is not None and not ready:
                    break
            self.assertEqual(termios.tcgetattr(slave), original)
            if interrupt:
                self.assertTrue(sent_signal)
            return process.returncode, collected.decode("utf-8", errors="replace")
        finally:
            if process.poll() is None:
                process.kill()
            process.wait(timeout=10)
            os.close(slave)
            os.close(master)

    def test_pulse_terminal_frames_and_plain_fallbacks(self) -> None:
        for scenario, settings, animated in (
            ("terminal", {}, True), ("narrow", {"COLUMNS":"20"}, True),
            ("dumb", {"TERM":"dumb"}, False), ("no-color", {"NO_COLOR":"1"}, False),
            ("raw", {"LEGO_OUTPUT":"raw"}, False),
        ):
            with self.subTest(scenario=scenario):
                status, output = self.terminal_issuance(**settings)
                self.assertEqual(status, 0, output)
                self.assertEqual("⠀⠶⠀" in output, animated, output)
                self.assertNotIn("test-private-token", output)
                if animated:
                    for frame in ("⠰⣿⠆", "⢾⣿⡷", "⢾⣉⡷", "⣏⣉⣹", "⡁⠀⢈"):
                        self.assertIn(frame, output)
                    plain = re.sub(r"\x1b\[[0-9;]*m", "", output)
                    # Every pulse state must appear on the domain row, rather
                    # than only beside the global elapsed-time counter.
                    for frame in ("⠀⠶⠀", "⠰⣿⠆", "⢾⣿⡷", "⢾⣉⡷", "⣏⣉⣹", "⡁⠀⢈"):
                        self.assertIn(frame + " example.com", plain)
                    if scenario == "terminal":
                        self.assertRegex(plain, "⢾⣿⡷ example.com +waiting for DNS propagation")
                        self.assertIn("✅  example.com", plain)
                    width = int(settings.get("COLUMNS", "100"))
                    for line in plain.split("\r\x1b[2K")[1:]:
                        line = line.split("\r", 1)[0].split("\n", 1)[0]
                        line = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", line)
                        if "⠀" in line or "⣿" in line:
                            self.assertLessEqual(len(line), width - 1)
                    if scenario == "terminal":
                        tail = output.split("Certificate received", 1)[1]
                        self.assertNotIn("⠀⠶⠀", tail)

    def test_pulse_signal_stops_animation_before_failure_summary(self) -> None:
        status, output = self.terminal_issuance(interrupt=True)
        self.assertEqual(status, 128 + signal.SIGTERM, output)
        self.assertIn("No certificate", output)
        self.assertNotIn("⠀⠶⠀", output.split("No certificate", 1)[1])

    def test_all_five_targets_have_real_debug_elf_binaries(self) -> None:
        for architecture, level, machine in (("amd64", "", 62), ("386", "", 3), ("arm", "6", 40), ("arm", "7", 40), ("arm64", "", 183)):
            with self.subTest(architecture=architecture, level=level):
                target = self.work / ("certgen-" + architecture + level)
                compile_go(target, "./cmd/certgen", GOOS="linux", GOARCH=architecture, GOARM=level, GOAMD64="v1")
                with target.open("rb") as stream:
                    header = stream.read(64)
                    # A CGO-free scratch executable must not ask the kernel for
                    # a dynamic interpreter, on either 32-bit or 64-bit targets.
                    if header[4] == 1:
                        offset = struct.unpack_from("<I", header, 28)[0]
                        size, count = struct.unpack_from("<HH", header, 42)
                    else:
                        offset = struct.unpack_from("<Q", header, 32)[0]
                        size, count = struct.unpack_from("<HH", header, 54)
                    for index in range(count):
                        stream.seek(offset + index * size)
                        self.assertNotEqual(struct.unpack("<I", stream.read(4))[0], 3,
                                            "The scratch binary requires a dynamic interpreter")
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
