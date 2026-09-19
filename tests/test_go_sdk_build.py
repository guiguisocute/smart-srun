"""Focused checks for release version mapping and failed signing/config input."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("build_go_sdk", ROOT / "scripts/build_go_sdk.py")
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class GoSDKBuildTests(unittest.TestCase):
    def test_payload_rejects_missing_binary_extra_config_and_legacy_runtime(self):
        files = dict.fromkeys(build.CORE_FILES | build.LUCI_FILES, 100)
        build.validate_payload("luci-app-smart-srun-bundle", files, 10 * 1024**2)
        for unwanted in ("etc/smart-srun/config.json", "usr/lib/smart_srun/client.py", "tests/secrets.json"):
            with self.subTest(unwanted=unwanted), self.assertRaises(ValueError):
                build.validate_payload("luci-app-smart-srun-bundle", {**files, unwanted: 1}, 10 * 1024**2)
        del files["usr/bin/srunnet"]
        with self.assertRaises(ValueError):
            build.validate_payload("luci-app-smart-srun-bundle", files, 10 * 1024**2)

    def test_rc_and_stable_native_versions(self):
        self.assertEqual(build.package_version("2.0.0rc10", "opkg"), "2.0.0~rc10")
        self.assertEqual(build.package_version("2.0.0rc10", "apk"), "2.0.0_rc10")
        self.assertEqual(build.package_version("2.0.0", "apk"), "2.0.0")
        for invalid in ("2.0.0rc0", "2.0.0rc01", "v2.0.0", "2.0.0\n", "2.0.0;touch x", "02.0.0"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                build.package_version(invalid, "opkg")

    def test_catalog_has_no_ambiguous_target_or_unpinned_commit(self):
        catalog = json.loads((ROOT / "targets.json").read_text())
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "targets.json"
            target = catalog["targets"][0]
            catalog["targets"].append(target)
            path.write_text(json.dumps(catalog))
            with self.assertRaises(ValueError):
                build.load_target(path, target["id"])
            catalog["targets"].pop()
            target["feeds"]["packages"] = "master"
            path.write_text(json.dumps(catalog))
            with self.assertRaises(ValueError):
                build.load_target(path, target["id"])

    def test_cached_archive_is_verified_before_reuse(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "sdk.tar.zst"
            path.write_bytes(b"corrupted")
            with patch.object(build.urllib.request, "urlopen") as network:
                with self.assertRaises(ValueError):
                    build.download("https://downloads.openwrt.org/test", path, "0" * 64)
                network.assert_not_called()

    def test_signer_zero_exit_does_not_hide_reported_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            key = Path(temporary) / "public.pem"
            key.write_text("test public input")
            with patch.object(build, "command", return_value="UNTRUSTED signature\n"):
                with self.assertRaisesRegex(RuntimeError, "signer reported"):
                    build.sign_apk("apk", "test.apk", "private.pem", key)

    def test_signing_requires_separate_trusted_verification(self):
        calls = []

        def command(args):
            calls.append(args)
            if "verify" in args:
                raise RuntimeError("untrusted signature")
            return ""

        with tempfile.TemporaryDirectory() as temporary:
            key = Path(temporary) / "public.pem"
            key.write_text("test public input")
            with patch.object(build, "command", side_effect=command):
                with self.assertRaisesRegex(RuntimeError, "untrusted signature"):
                    build.sign_apk("apk", "test.apk", "private.pem", key)
        self.assertEqual(len(calls), 2)
        self.assertIn("--allow-untrusted", calls[0])
        self.assertNotIn("--allow-untrusted", calls[1])


if __name__ == "__main__":
    unittest.main()
