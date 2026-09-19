"""Release evidence must stay attached to the exact package bytes."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("go_manifest", ROOT / "scripts/make_go_manifest.py")
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)


class ManifestTests(unittest.TestCase):
    def fixture(self, directory):
        package = Path(directory) / "smart-srun_2.0.0~rc2-r1_x86_64.ipk"
        package.write_bytes(b"measured test fixture")
        digest = hashlib.sha256(package.read_bytes()).hexdigest()
        record = dict(schema_version=1, display_version="2.0.0rc2", source_commit="a" * 40, source_dirty=False,
                      source_files={"Makefile": "a" * 64, "core/go.mod": "b" * 64},
                      source_template_files={"Makefile": "a" * 64, "core/go.mod": "b" * 64},
                      target=dict(package_manager="opkg", format="ipk", sdk_release="24.10.8",
                                  openwrt_arch="x86_64", target="x86/64", goos="linux", goarch="amd64"),
                      artifacts=[dict(file=package.name, name="smart-srun", package_version="2.0.0~rc2-r1",
                                      architecture="x86_64", bytes=package.stat().st_size, sha256=digest,
                                      installed_payload_bytes=100, files={"usr/bin/srunnet": 100}, validation={"build": True})])
        path = Path(directory) / "build-record.json"
        path.write_text(json.dumps(record))
        return path, package, record, digest

    def test_build_never_implies_runtime_acceptance(self):
        with tempfile.TemporaryDirectory() as directory:
            path, _, _, digest = self.fixture(directory)
            result, sums = manifest.generate([path])
            asset = result["assets"][0]
            self.assertEqual(asset["validation"], {key: key == "build" for key in manifest.CHECKS})
            self.assertIn(digest, sums)
            result, _ = manifest.generate([path], {"schema_version": 1, "assets": {digest: {"elf": True}}})
            self.assertTrue(result["assets"][0]["validation"]["elf"])
            self.assertFalse(result["assets"][0]["validation"]["campus_auth"])

    def test_corruption_dirty_build_and_ambiguous_selection_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            path, package, record, _ = self.fixture(directory)
            with self.assertRaises(ValueError):
                manifest.generate([path, path])
            record["source_dirty"] = True
            path.write_text(json.dumps(record))
            with self.assertRaises(ValueError):
                manifest.generate([path])
            manifest.generate([path], internal=True)
            package.write_bytes(b"modified after validation")
            with self.assertRaises(ValueError):
                manifest.generate([path], internal=True)

    def test_same_commit_with_different_worktree_bytes_cannot_mix_sdk_assets(self):
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            path, _, _, _ = self.fixture(first)
            other, _, record, _ = self.fixture(second)
            record["source_files"]["core/go.mod"] = "c" * 64
            record["source_template_files"]["core/go.mod"] = "c" * 64
            other.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "same source files"):
                manifest.generate([path, other])

    def test_only_makefile_may_differ_from_measured_source_template(self):
        with tempfile.TemporaryDirectory() as directory:
            path, _, record, _ = self.fixture(directory)
            record["source_files"]["Makefile"] = "c" * 64
            path.write_text(json.dumps(record))
            manifest.generate([path])
            record["source_files"]["core/go.mod"] = "c" * 64
            path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "substitution"):
                manifest.generate([path])
            del record["source_template_files"]
            path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "source file identities"):
                manifest.generate([path])

    def test_negative_file_sizes_and_non_native_targets_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            path, _, record, _ = self.fixture(directory)
            record["artifacts"][0]["files"] = {"usr/bin/srunnet": 110, "other": -10}
            path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "payload"):
                manifest.generate([path])
            record["target"]["openwrt_arch"] = "all"
            path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "target identity"):
                manifest.generate([path])


if __name__ == "__main__":
    unittest.main()
