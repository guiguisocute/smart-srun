"""Backups preserve secrets and reject invalid/stale imports before writing."""
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT/'root/usr/lib/smart_srun'))
import config  # noqa: E402
import config_backup as backup  # noqa: E402


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)/'config.json'
        self.patch = patch.object(config, 'JSON_CONFIG_FILE', str(self.path))
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.fixture = (ROOT/'tests/fixtures/config-backup-v1.json').read_bytes()
        self.raw = json.loads(self.fixture)['config']
        self.path.write_text(json.dumps(self.raw), encoding='utf-8')

    def test_roundtrip_preserves_credentials_suffix_and_advanced_values(self):
        before = self.path.read_bytes()
        exported = backup.export_backup()
        self.assertEqual(self.path.read_bytes(), before)
        parsed = backup.decode_backup(exported)
        for name in ('campus_accounts', 'hotspot_profiles', 'quiet_start', 'retry_cooldown_seconds'):
            self.assertEqual(parsed[name], self.raw[name])
        preview = backup.import_backup(exported, check_only=True)
        self.assertEqual(self.path.read_bytes(), before)
        report = backup.import_backup(exported, preview['expected_revision'])
        self.assertTrue(report['ok'])
        restored = json.loads(self.path.read_bytes())
        self.assertEqual(restored, dict(parsed, enabled='0'))
        self.assertNotIn('synthetic-secret', json.dumps(report))
        if os.name != 'nt':
            self.assertEqual(stat.S_IMODE(self.path.stat().st_mode), 0o600)

    def test_stale_preview_never_overwrites_new_configuration(self):
        preview = backup.import_backup(self.fixture, check_only=True)
        changed = dict(self.raw, interval='31')
        self.path.write_text(json.dumps(changed))
        before = self.path.read_bytes()
        with self.assertRaisesRegex(ValueError, '其他页面'):
            backup.import_backup(self.fixture, preview['expected_revision'])
        self.assertEqual(self.path.read_bytes(), before)

    def test_malformed_backups_leave_original_bytes(self):
        before = self.path.read_bytes()
        invalid = [b'{}', b'[]', self.fixture+b'{}', b'x'*(backup.MAX_BYTES+1),
                   self.fixture.replace(b'"format_version": 1', b'"format_version": 1, "format_version": 1'),
                   self.fixture.replace(b'"config_schema": 1', b'"config_schema": 2'),
                   self.fixture.replace(b'"campus_accounts": [', b'"unexpected": "value", "campus_accounts": [')]
        for data in invalid:
            with self.subTest(size=len(data)), self.assertRaises(ValueError):
                backup.import_backup(data)
            self.assertEqual(self.path.read_bytes(), before)

    def test_rename_failure_retains_original_and_removes_owned_temp(self):
        before = self.path.read_bytes()
        with patch.object(config.os, 'replace', side_effect=OSError('synthetic failure')):
            with self.assertRaises(OSError):
                backup.import_backup(self.fixture)
        self.assertEqual(self.path.read_bytes(), before)
        self.assertEqual(list(self.path.parent.glob('*.tmp.*')), [])

    def test_export_file_never_clobbers_and_is_private(self):
        path = self.path.parent/'backup.json'
        backup.write_export(path, self.fixture)
        with self.assertRaises(FileExistsError):
            backup.write_export(path, b'changed')
        self.assertEqual(path.read_bytes(), self.fixture)
        if os.name != 'nt':
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)

    def test_hostile_temp_path_never_overwrites_target(self):
        path = Path(str(self.path)+'.tmp.'+str(os.getpid()))
        path.write_bytes(b'unrelated file')
        before = self.path.read_bytes()
        with self.assertRaises(FileExistsError):
            backup.import_backup(self.fixture)
        self.assertEqual(path.read_bytes(), b'unrelated file')
        self.assertEqual(self.path.read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
