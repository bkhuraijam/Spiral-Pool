"""Proof tests for the Sentinel auto-restart cooldown fix.

The offline auto-restart gate used to read get_last_restart_time(), which is only
written after a SUCCESSFUL restart. A miner that cannot be restarted remotely
never recorded anything, so it was retried on every monitor cycle for as long as
it stayed offline. The attempt is now recorded separately, before it is made:

  - MonitorState.record_restart_attempt / get_last_restart_attempt_time
  - failed attempts must NOT count towards restart stats or excessive-restart alerts
  - the attempt map must persist and be pruned like the other per-miner dicts

Run: python -m pytest tests/test_restart_cooldown.py -v
"""
import importlib.util
import os
import tempfile

os.environ.setdefault("SPIRALPOOL_INSTALL_DIR", tempfile.mkdtemp())
_spec = importlib.util.spec_from_file_location(
    "spiral_sentinel",
    os.path.join(os.path.dirname(__file__), "..", "src", "sentinel", "SpiralSentinel.py"),
)
sentinel = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(sentinel)


def _state():
    """A bare MonitorState without running __init__ (which does file I/O)."""
    s = sentinel.MonitorState.__new__(sentinel.MonitorState)
    s.miner_restart_times = {}
    s.miner_restart_attempts = {}
    s.miner_hashrate_baseline = {}
    return s


class Clock:
    """Controllable replacement for time.time() during a test."""
    def __init__(self, start=1_000_000.0):
        self.t = start
        self._orig = sentinel.time.time

    def __enter__(self):
        sentinel.time.time = lambda: self.t
        return self

    def __exit__(self, *a):
        sentinel.time.time = self._orig


COOL = 1800  # auto_restart_cooldown default


def _due(state, name, now, cooldown=COOL):
    """The gate the monitor loop applies before attempting an auto-restart."""
    return (now - state.get_last_restart_attempt_time(name)) > cooldown


class TestAttemptCooldown:
    def test_no_history_is_due(self):
        s = _state()
        assert s.get_last_restart_attempt_time("rig") == 0
        assert _due(s, "rig", 1_000_000.0)

    def test_failed_attempt_still_starts_the_cooldown(self):
        """The bug: a restart that does not work must not be retried next cycle."""
        s = _state()
        with Clock() as c:
            s.record_restart_attempt("rig")          # restart_miner() returned False
            assert not _due(s, "rig", c.t)           # same cycle
            assert not _due(s, "rig", c.t + 60)      # next monitor cycle
            assert not _due(s, "rig", c.t + COOL)    # boundary: strictly greater required
            assert _due(s, "rig", c.t + COOL + 1)    # cooldown elapsed — try again

    def test_successful_restart_also_gates(self):
        s = _state()
        with Clock() as c:
            s.record_miner_restart("rig")
            assert not _due(s, "rig", c.t + 60)
            assert _due(s, "rig", c.t + COOL + 1)

    def test_takes_the_later_of_attempt_and_success(self):
        s = _state()
        with Clock() as c:
            s.record_miner_restart("rig")            # succeeded long ago
            c.t += 5000
            s.record_restart_attempt("rig")          # failed just now
            assert s.get_last_restart_attempt_time("rig") == c.t
            assert not _due(s, "rig", c.t + 60)

            c.t += 5000
            s.record_miner_restart("rig")            # success is newer again
            assert s.get_last_restart_attempt_time("rig") == c.t

    def test_legacy_scalar_restart_times_still_read(self):
        s = _state()
        s.miner_restart_times["rig"] = 1_000_000.0   # pre-list state.json format
        assert s.get_last_restart_attempt_time("rig") == 1_000_000.0

    def test_corrupt_attempt_value_is_ignored(self):
        s = _state()
        s.miner_restart_attempts["rig"] = "not-a-timestamp"
        assert s.get_last_restart_attempt_time("rig") == 0
        assert _due(s, "rig", 1_000_000.0)


class TestFailedAttemptsAreNotRestarts:
    """Stats, badges and the excessive-restart alert count restarts that happened."""

    def test_attempt_does_not_add_to_restart_count(self):
        s = _state()
        with Clock() as c:
            for _ in range(5):
                s.record_restart_attempt("rig")
                c.t += 60
            count, times = s.get_restart_frequency("rig")
            assert count == 0 and times == []

    def test_attempt_does_not_trigger_excessive_restarts(self):
        s = _state()
        with Clock() as c:
            for _ in range(5):
                s.record_restart_attempt("rig")
                c.t += 60
            assert s.check_excessive_restarts("rig") == (False, 0, 1)

    def test_successes_still_trigger_excessive_restarts(self):
        s = _state()
        with Clock() as c:
            for _ in range(4):
                s.record_miner_restart("rig")
                c.t += 60
            assert s.check_excessive_restarts("rig") == (True, 4, 1)


class TestStateWiring:
    def test_attempts_are_persisted(self):
        assert "miner_restart_attempts" in sentinel.MonitorState._PERSIST_KEYS

    def test_attempts_are_pruned_with_the_other_per_miner_dicts(self):
        s = sentinel.MonitorState.__new__(sentinel.MonitorState)
        sentinel.MonitorState._init_state(s)
        s.miner_restart_attempts = {"gone": 1_000_000.0, "kept": 1_000_000.0}
        s.prune_stale_miner_state({"kept"})
        assert list(s.miner_restart_attempts) == ["kept"]
