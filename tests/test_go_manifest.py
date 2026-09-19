"""Release evidence must stay attached to the exact package bytes."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

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

    def apk_fixture(self, directory, arch, goarch):
        Path(directory).mkdir()
        path, old, record, _ = self.fixture(directory)
        record["target"].update(package_manager="apk", format="apk", sdk_release="25.12.2",
                                openwrt_arch=arch, goarch=goarch)
        asset = record["artifacts"][0]
        asset.update(file="smart-srun-2.0.0_rc2-r1.apk", package_version="2.0.0_rc2-r1",
                     architecture=arch, signature_spki_sha256="c" * 64)
        package = old.with_name(asset["file"])
        package.write_bytes(b"synthetic signed package " + arch.encode())
        asset.update(bytes=package.stat().st_size, sha256=hashlib.sha256(package.read_bytes()).hexdigest())
        old.unlink()
        path.write_text(json.dumps(record))
        return path, package, record

    def test_export_distinguishes_same_native_apk_name_and_preserves_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first, pkg1, _ = self.apk_fixture(root / "x86", "x86_64", "amd64")
            second, pkg2, _ = self.apk_fixture(root / "arm", "aarch64_cortex-a53", "arm64")
            self.assertEqual(pkg1.name, pkg2.name)
            out = root / "release"
            result = manifest.export_release([first, second], out)
            exported = [asset["url"].rsplit("/", 1)[1] for asset in result["assets"]]
            self.assertEqual(len(set(exported)), 2)
            for name, package in zip(exported, (pkg1, pkg2)):
                self.assertIn("_openwrt-25.12.2.apk", name)
                self.assertEqual((out / name).read_bytes(), package.read_bytes())
            for line in (out / "SHA256SUMS").read_text().splitlines():
                digest, name = line.split("  ")
                self.assertEqual(hashlib.sha256((out / name).read_bytes()).hexdigest(), digest)
            with self.assertRaisesRegex(ValueError, "existing release directory"):
                manifest.export_release([first, second], out)

    def test_export_distinguishes_sdk_families_and_rejects_copy_corruption(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "first").mkdir(); (root / "second").mkdir()
            first, _, _, _ = self.fixture(root / "first")
            second, _, record, _ = self.fixture(root / "second")
            record["target"]["sdk_release"] = "23.05.6"
            second.write_text(json.dumps(record))
            result, _ = manifest.generate([first, second])
            self.assertEqual(len({asset["url"] for asset in result["assets"]}), 2)
            out = root / "release"
            with patch.object(manifest.shutil, "copyfile", side_effect=lambda src, dst: dst.write_bytes(b"changed")):
                with self.assertRaisesRegex(ValueError, "changed while staging"):
                    manifest.export_release([first, second], out)
            self.assertFalse(out.exists())

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
