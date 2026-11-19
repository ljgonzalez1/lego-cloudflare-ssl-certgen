#!/usr/bin/env python3
"""Exercise the real C++ menu with a pseudo-terminal."""

import errno
import os
import pty
import select
import signal
import subprocess
import sys
import termios
import time
import unittest
from contextlib import contextmanager

CORE = sys.argv.pop(1)
LEFT = b"\x1b[D"
RIGHT = b"\x1b[C"
ENTER = b"\r"
MAIN = b"Choose an issuance environment"
PRODUCTION = b"Confirm production issuance"
STAGING = b"Confirm staging issuance"


class TerminalSession:
    def __init__(self, master, slave, process):
        self.master = master
        self.slave = slave
        self.process = process
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

    def result(self):
        stdout, _ = self.process.communicate(timeout=5)
        return self.process.returncode, stdout


@contextmanager
def terminal(command, *, term="xterm", env=None, stdout=subprocess.PIPE):
    master, slave = pty.openpty()
    settings = os.environ.copy()
    settings.pop("NO_COLOR", None)
    settings["TERM"] = term
    settings.update(env or {})
    try:
        process = subprocess.Popen(
            command, stdin=slave, stdout=stdout, stderr=slave,
            env=settings, start_new_session=True,
        )
        try:
            yield TerminalSession(master, slave, process)
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


if __name__ == "__main__":
    unittest.main()
