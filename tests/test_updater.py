import os
import sys
import tempfile
import unittest
import zipfile
from unittest import mock


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MODULE_ROOT = os.path.join(REPO_ROOT, "root", "usr", "lib", "smart_srun")

if MODULE_ROOT not in sys.path:
    sys.path.insert(0, MODULE_ROOT)


import updater


class UpdaterTests(unittest.TestCase):
    def test_build_update_plan_selects_matching_bundle_asset(self):
        release = {
            "tag_name": "v1.3.4",
            "assets": [
                {
                    "name": "luci-app-smart-srun-bundle_1.3.4-r1_all.ipk",
                    "browser_download_url": "https://example.invalid/bundle.ipk",
                    "digest": "sha256:abc",
                }
            ],
        }
        with (
            mock.patch.object(
                updater.version_info,
                "detect_installed_package_name",
                return_value="luci-app-smart-srun-bundle",
            ),
            mock.patch.object(
                updater.version_info,
                "get_display_version",
                return_value="v1.3.3-r1",
            ),
            mock.patch.object(updater, "package_manager", return_value="opkg"),
        ):
            plan = updater.build_update_plan(release)

        self.assertEqual(plan["install_mode"], "bundle")
        self.assertEqual(plan["package_format"], "ipk")
        self.assertTrue(plan["update_available"])
        self.assertEqual(plan["asset_name"], "luci-app-smart-srun-bundle_1.3.4-r1_all.ipk")
        self.assertEqual(plan["download_url"], "https://example.invalid/bundle.ipk")

    def test_build_update_plan_uses_downloads_branch_for_split_packages(self):
        release = {"tag_name": "v1.3.4", "assets": []}
        with (
            mock.patch.object(
                updater.version_info,
                "detect_installed_package_name",
                return_value="luci-app-smart-srun",
            ),
            mock.patch.object(
                updater.version_info,
                "get_display_version",
                return_value="v1.3.3-r1",
            ),
            mock.patch.object(updater, "package_manager", return_value="opkg"),
        ):
            plan = updater.build_update_plan(release)

        self.assertEqual(plan["install_mode"], "split")
        self.assertEqual(plan["download_kind"], "split_zip")
        self.assertEqual(
            plan["download_urls"],
            [
                "https://raw.githubusercontent.com/matthewlu070111/"
                "smart-srun/downloads/1.3.4/smart-srun-split-packages-1.3.4.zip"
            ],
        )

    def test_split_zip_extraction_rejects_path_traversal(self):
        with tempfile.TemporaryDirectory() as tmp:
            zip_path = os.path.join(tmp, "bad.zip")
            with zipfile.ZipFile(zip_path, "w") as archive:
                archive.writestr("../bad.ipk", "bad")

            with self.assertRaisesRegex(RuntimeError, "unsafe split zip member"):
                updater._extract_split_zip(zip_path, os.path.join(tmp, "out"), "ipk", "split")

    def test_split_zip_extraction_selects_core_and_luci_packages(self):
        with tempfile.TemporaryDirectory() as tmp:
            zip_path = os.path.join(tmp, "split.zip")
            with zipfile.ZipFile(zip_path, "w") as archive:
                archive.writestr("smart-srun_1.3.4-r1_all.ipk", "core")
                archive.writestr("luci-app-smart-srun_1.3.4-r1_all.ipk", "luci")
                archive.writestr("luci-app-smart-srun-bundle_1.3.4-r1_all.ipk", "bundle")
            out_dir = os.path.join(tmp, "out")
            os.mkdir(out_dir)

            selected = updater._extract_split_zip(zip_path, out_dir, "ipk", "split")

        self.assertEqual(len(selected), 2)
        self.assertTrue(any(os.path.basename(path).startswith("smart-srun_") for path in selected))
        self.assertTrue(
            any(os.path.basename(path).startswith("luci-app-smart-srun_") for path in selected)
        )
        self.assertFalse(any("bundle" in os.path.basename(path) for path in selected))


if __name__ == "__main__":
    unittest.main()
