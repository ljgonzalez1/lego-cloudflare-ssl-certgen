#!/usr/bin/env python3
"""Exercise the real C++ menu with a pseudo-terminal."""

import errno
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import unittest
from contextlib import contextmanager
from pathlib import Path
from tempfile import TemporaryDirectory

CORE = sys.argv.pop(1)
ENTRY = sys.argv.pop(1)
LEFT = b"\x1b[D"
RIGHT = b"\x1b[C"
ENTER = b"\r"
MAIN = b"Choose an issuance environment"
PRODUCTION = b"Confirm production issuance"
STAGING = b"Confirm staging issuance"


class TerminalSession:
    def __init__(self, master, slave, process, original):
        self.master = master
        self.slave = slave
        self.process = process
        self.original = original
        self.buffer = b""

    def read_until(self, expected):
        deadline = time.monotonic() + 5
        while expected not in self.buffer:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([self.master], [], [], remaining)[0]:
                raise AssertionError(f"Missing {expected!r} in {self.buffer[-4000:]!r}")
            try:
                chunk = os.read(self.master, 65536)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise
                chunk = b""
            if not chunk:
                raise AssertionError(f"Terminal closed before {expected!r}: {self.buffer[-4000:]!r}")
            self.buffer += chunk
        frame, self.buffer = self.buffer.split(expected, maxsplit=1)
        return frame + expected

    def send(self, keys):
        os.write(self.master, keys)

    def assert_restored(self):
        if termios.tcgetattr(self.slave) != self.original:
            raise AssertionError("The menu did not restore the terminal settings")

    def result(self):
        stdout, _ = self.process.communicate(timeout=5)
        return self.process.returncode, stdout


@contextmanager
def terminal(command, *, term="xterm", env=None, columns=80):
    master, slave = pty.openpty()
    settings = os.environ.copy()
    settings.pop("NO_COLOR", None)
    settings["TERM"] = term
    settings.update(env or {})
    try:
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, columns, 0, 0))
        original = termios.tcgetattr(slave)
        process = subprocess.Popen(
            command, stdin=slave, stdout=subprocess.PIPE, stderr=slave,
            env=settings, start_new_session=True,
        )
        try:
            yield TerminalSession(master, slave, process, original)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
            if process.stdout is not None:
                process.stdout.close()
    finally:
        os.close(slave)
        os.close(master)


class MenuTests(unittest.TestCase):
    def test_production_requires_two_choices(self):
        with terminal([CORE, "issuance-menu"]) as session:
            session.read_until(MAIN)
            first = session.read_until(b"> Staging <")
            self.assertIn(b"\x1b[1;7m > Staging <", first)

            session.send(RIGHT)
            session.read_until(b"> Production <")
            session.send(ENTER)
            session.read_until(PRODUCTION)
            confirmation = session.read_until(b"> No <")
            self.assertIn(b"per registered domain", confirmation)
            session.send(LEFT)
            session.read_until(b"> Yes <")
            session.send(ENTER)
            self.assertEqual(session.result(), (0, b"production\n"))

    def test_staging_requires_confirmation(self):
        with terminal([CORE, "issuance-menu"]) as session:
            session.read_until(b"> Staging <")
            session.send(ENTER)
            session.read_until(STAGING)
            session.read_until(b"> No <")
            session.send(LEFT)
            session.read_until(b"> Yes <")
            session.send(ENTER)
            self.assertEqual(session.result(), (0, b"staging\n"))

    def test_no_on_confirmation_returns_to_menu(self):
        with terminal([CORE, "issuance-menu"]) as session:
            session.read_until(b"> Staging <")
            session.send(ENTER)
            session.read_until(STAGING)
            session.read_until(b"> No <")
            session.send(ENTER)
            session.read_until(MAIN)
            session.read_until(b"> Staging <")
            session.send(LEFT)
            session.read_until(b"> Cancel <")
            session.send(ENTER)
            self.assertEqual(session.result(), (0, b"cancel\n"))


    def test_plain_text_and_narrow_frame(self):
        for settings in ({"term": "dumb"}, {"term": "unknown"}, {"env": {"NO_COLOR": "1"}}):
            with self.subTest(settings=settings), terminal([CORE, "issuance-menu"], columns=22, **settings) as session:
                frame = session.read_until(b"> Staging <")
                self.assertNotIn(b"\x1b[", frame)
                self.assertTrue(any(line.startswith(b"+-") for line in frame.splitlines()))
                self.assertTrue(all(len(line) <= 21 for line in frame.splitlines()))
                session.send(LEFT)
                session.read_until(b"> Cancel <")
                session.send(ENTER)
                self.assertEqual(session.result(), (0, b"cancel\n"))
                session.assert_restored()

    def test_escape_leaves_confirmation_without_issuing(self):
        with terminal([CORE, "issuance-menu"]) as session:
            session.read_until(b"> Staging <")
            session.send(RIGHT)
            session.read_until(b"> Production <")
            session.send(ENTER)
            session.read_until(PRODUCTION)
            session.read_until(b"> No <")
            session.send(b"\x1b")
            session.read_until(MAIN)
            session.read_until(b"> Staging <")
            session.send(b"\x1b")
            self.assertEqual(session.result(), (0, b"cancel\n"))

    def test_interruptions_restore_the_terminal(self):
        for key, expected in ((b"\x03", (130, b"")), (b"\x04", (0, b"cancel\n")), (None, (143, b""))):
            with self.subTest(key=key), terminal([CORE, "issuance-menu"]) as session:
                session.read_until(b"> Staging <")
                if key is None:
                    session.process.send_signal(signal.SIGTERM)
                else:
                    session.send(key)
                self.assertEqual(session.result(), expected)
                session.read_until(b"\x1b[?1049l")
                session.assert_restored()

    def test_non_terminal_fails_closed(self):
        process = subprocess.run(
            [CORE, "issuance-menu"], input=b"\x1b[C\r\x1b[D\r",
            capture_output=True, timeout=5,
        )
        self.assertEqual(process.returncode, 2)
        self.assertEqual(process.stdout, b"")
        self.assertIn(b"docker run -it", process.stderr)

    def test_entrypoint_uses_only_the_confirmed_server(self):
        for production in (False, True):
            with self.subTest(production=production), TemporaryDirectory() as temp:
                command, env, output, orders = self.fixture(Path(temp))
                with terminal(command, env=env) as session:
                    session.read_until(b"> Staging <")
                    if production:
                        session.send(RIGHT)
                        session.read_until(b"> Production <")
                    session.send(ENTER)
                    session.read_until(PRODUCTION if production else STAGING)
                    session.read_until(b"> No <")
                    self.assertFalse(orders.exists())
                    self.assertFalse(list(output.iterdir()))
                    session.send(LEFT)
                    session.read_until(b"> Yes <")
                    session.send(ENTER)
                    status, _ = session.result()
                    self.assertEqual(status, 0)
                selected = "https://acme-v02" if production else "https://acme-staging-v02"
                self.assertIn(selected, orders.read_text())
                self.assertEqual(orders.read_text().count("order\n"), 1)
                self.assertTrue(list(output.rglob("example.com.crt")))

    def test_entrypoint_cancel_has_no_side_effects(self):
        with TemporaryDirectory() as temp:
            command, env, output, orders = self.fixture(Path(temp))
            with terminal(command, env=env) as session:
                session.read_until(b"> Staging <")
                session.send(LEFT)
                session.read_until(b"> Cancel <")
                session.send(ENTER)
                self.assertEqual(session.result()[0], 0)
            self.assertFalse(orders.exists())
            self.assertFalse(list(output.iterdir()))


    def test_paste_cannot_confirm_production(self):
        with terminal([CORE, "issuance-menu"]) as session:
            self.assertIn(b"\x1b[?2004h", session.read_until(b"> Staging <"))
            session.send(RIGHT)
            session.read_until(b"> Production <")
            session.send(ENTER)
            session.read_until(PRODUCTION)
            session.read_until(b"> No <")
            session.send(b"\x1b[200~" + LEFT + ENTER + b"\x1b[201~" + ENTER)
            # The last Enter accepts the still-selected No, returning to the menu.
            session.read_until(MAIN)
            session.read_until(b"> Staging <")
            session.send(b"\x04")
            self.assertEqual(session.result(), (0, b"cancel\n"))
            session.read_until(b"\x1b[?2004l")
            session.assert_restored()

    def test_ctrl_c_can_interrupt_incomplete_paste(self):
        with terminal([CORE, "issuance-menu"]) as session:
            session.read_until(b"> Staging <")
            session.send(b"\x1b[200~" + RIGHT + ENTER + b"\x03")
            self.assertEqual(session.result(), (130, b""))
            session.assert_restored()

    def test_noninteractive_flags_select_the_correct_server(self):
        for flag, expected in (("--staging", "acme-staging-v02"), ("--production", "acme-v02")):
            with self.subTest(flag=flag), TemporaryDirectory() as temp:
                command, env, output, orders = self.fixture(Path(temp))
                process = subprocess.run(command + [flag], env=os.environ | env, input=b"",
                                         capture_output=True, timeout=5)
                self.assertEqual(process.returncode, 0, process.stderr)
                self.assertIn(expected, orders.read_text())
                self.assertTrue(list(output.rglob("example.com.crt")))
                if flag == "--production":
                    self.assertIn(b"without confirmation", process.stderr)
                else:
                    self.assertNotIn(b"without confirmation", process.stderr)

    def test_conflicts_typos_and_inherited_mode_cannot_issue(self):
        cases = (
            (["--production", "--staging"], b"mutually exclusive"),
            (["--test", "--production"], b"cannot be combined"),
            (["--production", "--test"], b"cannot be combined"),
            (["--staging", "--list"], b"cannot be combined"),
            (["--dry-run", "--production"], b"cannot be combined"),
            (["--prodution"], b"Unknown argument"),
            ([], b"The issuance menu needs a terminal"),
        )
        with TemporaryDirectory() as temp:
            command, env, output, orders = self.fixture(Path(temp))
            env.update(CERTGEN_ACTION="issue", CERTGEN_MODE="production")
            for flags, diagnostic in cases:
                with self.subTest(flags=flags):
                    process = subprocess.run(command + flags, env=os.environ | env, input=b"yes\n",
                                             capture_output=True, timeout=5)
                    self.assertEqual(process.returncode, 2, process.stderr)
                    self.assertIn(diagnostic, process.stderr)
                    self.assertFalse(orders.exists())
                    self.assertFalse(list(output.iterdir()))

    def test_wrong_container_user_is_reported_before_prompting(self):
        with TemporaryDirectory() as temp:
            command, env, output, orders = self.fixture(Path(temp))
            env["UID"] = str(os.geteuid() + 1)
            process = subprocess.run(command, env=os.environ | env, input=b"", capture_output=True, timeout=5)
            self.assertEqual(process.returncode, 1, process.stderr)
            self.assertIn(b"The container user must match the configured UID/GID", process.stderr)
            self.assertFalse(orders.exists())
            self.assertFalse(list(output.iterdir()))

    def test_interrupted_issuance_stops_the_group_and_never_starts_another(self):
        for interruption in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            with self.subTest(interruption=interruption), TemporaryDirectory() as temp:
                root = Path(temp)
                command, env, _, orders = self.fixture(root)
                (root / "domains.yaml").write_text("running: [hang.example.org]\nnext: [example.net]\n")
                ready = root / "ready"
                env["TEST_LEGO_READY_FILE"] = str(ready)
                with subprocess.Popen(command + ["--staging"], env=os.environ | env, stdin=subprocess.DEVNULL,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True) as app:
                    children = []
                    try:
                        deadline = time.monotonic() + 5
                        while not children and time.monotonic() < deadline and app.poll() is None:
                            if ready.exists():
                                children = list(map(int, ready.read_text().split()))
                            time.sleep(0.01)
                        self.assertEqual(len(children), 2)
                        app.send_signal(interruption)
                        _, errors = app.communicate(timeout=6)
                        self.assertEqual(app.returncode, 128 + interruption, errors)
                        self.assertIn(b"Certificate issuance cancelled", errors)
                        self.assertNotIn("example.net", orders.read_text())
                        for pid in children:
                            status = Path(f"/proc/{pid}/status")
                            # SIGKILL may still be pending briefly after the supervisor exits.
                            deadline = time.monotonic() + 1
                            while time.monotonic() < deadline:
                                try:
                                    state = status.read_text()
                                except FileNotFoundError:
                                    break
                                if "State:\tZ" in state:
                                    break
                                time.sleep(0.01)
                            else:
                                self.fail(f"Child {pid} is still running after cancellation")
                    finally:
                        if app.poll() is None:
                            app.kill()
                            for pid in children:
                                try:
                                    os.kill(pid, signal.SIGKILL)
                                except ProcessLookupError:
                                    pass

    @staticmethod
    def fixture(root):
        config = root / "domains.yaml"
        config.write_text("certificate: [example.com, example.net]\n")
        output = root / "certificates"
        output.mkdir()
        # A legacy deployment setting cannot pick a server.
        orders = root / "orders"
        env = {
            "TEST_LEGO_LOG": str(orders),
            "PRODUCTION": "true",
        }
        return [ENTRY, "--config", str(config), "--output", str(output)], env, output, orders


if __name__ == "__main__":
    unittest.main()
