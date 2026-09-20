"""Release automation must refuse wrong identities and preserve verified bytes."""
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import ci_go_release as release  # noqa: E402
import check_go_source as source  # noqa: E402


class ReleaseAutomationTests(unittest.TestCase):
    def test_versions_reject_shell_paths_and_wrong_channels(self):
        for value in ['1.6.0', 'v2.0.0', '2.0.0rc0', '2.0.0;echo bad', '../../2.0.0', '2.0.0\n']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                release.validate_version(value)
        self.assertEqual(release.validate_version('2.0.0rc10', 'rc'), '2.0.0rc10')
        self.assertEqual(release.validate_version('2.0.0', 'stable'), '2.0.0')
        with self.assertRaises(ValueError):
            release.validate_version('2.0.0rc2', 'stable')

    def test_official_apk_without_key_fails_before_build(self):
        target = next(t for t in release.catalog()['targets'] if t['format'] == 'apk')
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True), patch.object(release.sdk, 'build') as build:
            args = SimpleNamespace(version='2.0.0rc1', target=target['id'], official=True,
                                   work=Path(temp)/'work', output=Path(temp)/'out')
            with self.assertRaisesRegex(ValueError, 'requires SMARTSRUN_APK_SIGNING_KEY'):
                release.build(args)
            build.assert_not_called()
            self.assertFalse((args.work/'signing/private.pem').exists())

    def test_missing_matrix_and_preview_promotion_fail_closed(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(release, 'run', return_value='a'*40):
            root = Path(temp)
            args = SimpleNamespace(input=root, output=root/'release', version='2.0.0rc1', official=True)
            with self.assertRaisesRegex(ValueError, 'complete pinned SDK matrix'):
                release.assemble(args)
            folder = root/'one'
            folder.mkdir()
            (folder/'build-record.json').write_text(json.dumps({
                'source_commit': 'a'*40, 'source_dirty': False, 'display_version': args.version,
                'target': release.catalog()['targets'][0]}))
            (folder/'ci.json').write_text(json.dumps({'official_signing': False}))
            with self.assertRaisesRegex(ValueError, 'Preview artifacts'):
                release.assemble(args)
            self.assertFalse(args.output.exists())

    def test_split_zip_and_all_extra_checksums_preserve_exact_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = root/'out'
            out.mkdir()
            records = root/'records'
            records.mkdir()
            (records/'build-record.json').write_text(json.dumps({'target': {'id': 'synthetic-target'}}))
            for name in ['validation.json', 'inspection.json', 'ci.json']:
                (records/name).write_text('{}')
            key = root/'public.pem'
            key.write_text('synthetic public key; not usable for installation')
            assets = []
            for kind in ['core', 'luci']:
                filename = kind+'.ipk'
                (out/filename).write_bytes((kind+' exact bytes').encode())
                assets.append({'kind': kind, 'package_manager': 'opkg', 'sdk_release': '24.10.8',
                               'openwrt_arch': 'x86_64' if kind == 'core' else 'all',
                               'url': 'https://example.test/'+filename})
            data = {'release': '2.0.0rc1', 'source_commit': 'a'*40, 'assets': assets}
            release.add_release_extras(data, [records/'build-record.json'], [key], {'b'*64},
                                      SimpleNamespace(output=out, official=True))
            with zipfile.ZipFile(next(out.glob('*.zip'))) as archive:
                self.assertEqual(archive.namelist(), ['core.ipk', 'luci.ipk'])
                self.assertEqual(archive.read('core.ipk'), b'core exact bytes')
            for line in (out/'SHA256SUMS').read_text().splitlines():
                digest, name = line.split('  ')
                self.assertEqual(digest, hashlib.sha256((out/name).read_bytes()).hexdigest())
            self.assertNotIn('${', (out/'release-notes.md').read_text(encoding='utf-8'))

    def test_source_audit_allows_host_tools_but_rejects_runtime_and_private_files(self):
        paths = ['scripts/ci_go_release.py', 'tests/test_ci_go_release.py', 'root/usr/lib/smart_srun/cli.py',
                 '.codex/private.json', 'root/etc/smart-srun/config.json', 'signing.key', 'old.apk']
        self.assertEqual(source.inspect(paths), sorted(paths[2:]))

    def test_workflows_pin_actions_and_use_verified_candidate(self):
        import yaml
        for path in (ROOT/'.github/workflows').glob('*.yml'):
            workflow = yaml.safe_load(path.read_text())
            for job in workflow['jobs'].values():
                for step in job.get('steps', []):
                    uses = step.get('uses', '')
                    if uses and not uses.startswith('./'):
                        self.assertRegex(uses, r'^[\w/-]+@[0-9a-f]{40}$')
        publish = (ROOT/'.github/workflows/publish-go.yml').read_text()
        self.assertIn('sha256sum --strict --check', publish)
        self.assertIn('--draft --latest=false', publish)
        self.assertNotIn('--clobber', publish)
        self.assertNotIn('build_go_sdk.py', publish)


if __name__ == '__main__':
    unittest.main()
