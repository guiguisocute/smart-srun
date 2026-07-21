"""切网自动守护暂停/恢复（switch_service_guard）单元测试。

LuCI 切热点时置 enabled=0 并在 state 里记 guard；守护进程检测到回到校园网后
调用 restore_switch_service_guard 恢复。这里验证恢复助手本身的行为。
"""

import json
import os
import shutil
import sys
import tempfile
import unittest

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
LIB_DIR = REPO_ROOT / "root" / "usr" / "lib" / "smart_srun"

if str(LIB_DIR) not in sys.path:
    sys.path.insert(0, str(LIB_DIR))

import config  # noqa: E402


class SwitchServiceGuardTests(unittest.TestCase):
    def setUp(self):
        self.tmp_dir = tempfile.mkdtemp(prefix="switch-guard-")
        self.config_path = os.path.join(self.tmp_dir, "config.json")
        self.state_path = os.path.join(self.tmp_dir, "state.json")
        self.original_config_file = config.JSON_CONFIG_FILE
        self.original_state_file = config.STATE_FILE
        config.JSON_CONFIG_FILE = self.config_path
        config.STATE_FILE = self.state_path

    def tearDown(self):
        config.JSON_CONFIG_FILE = self.original_config_file
        config.STATE_FILE = self.original_state_file
        shutil.rmtree(self.tmp_dir)

    def _write_state(self, payload):
        with open(self.state_path, "w", encoding="utf-8") as handle:
            json.dump(payload, handle)

    def test_restore_reenables_service_and_clears_guard(self):
        config.save_json_raw_config({"enabled": "0"})
        self._write_state(
            {
                "switch_service_guard_active": True,
                "switch_service_enabled_before": "1",
            }
        )

        restored, previous_enabled = config.restore_switch_service_guard()

        self.assertTrue(restored)
        self.assertEqual("1", previous_enabled)
        self.assertEqual("1", config.load_json_raw_config()["enabled"])
        state = config.load_runtime_state()
        self.assertFalse(state["switch_service_guard_active"])
        self.assertEqual("", state["switch_service_enabled_before"])

    def test_restore_is_noop_without_active_guard(self):
        config.save_json_raw_config({"enabled": "0"})
        self._write_state({"switch_service_guard_active": False})

        restored, previous_enabled = config.restore_switch_service_guard()

        self.assertFalse(restored)
        self.assertEqual("", previous_enabled)
        self.assertEqual("0", config.load_json_raw_config()["enabled"])

    def test_restore_defaults_to_enabled_when_before_value_missing(self):
        config.save_json_raw_config({"enabled": "0"})
        self._write_state({"switch_service_guard_active": True})

        restored, previous_enabled = config.restore_switch_service_guard()

        self.assertTrue(restored)
        self.assertEqual("1", previous_enabled)
        self.assertEqual("1", config.load_json_raw_config()["enabled"])


if __name__ == "__main__":
    unittest.main()
