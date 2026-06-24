import importlib
import json
import os
import sys
import tempfile
import unittest
from unittest import mock


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MODULE_ROOT = os.path.join(REPO_ROOT, "root", "usr", "lib", "smart_srun")

if MODULE_ROOT not in sys.path:
    sys.path.insert(0, MODULE_ROOT)


import config
import school_presets


class SchoolPresetTests(unittest.TestCase):
    def test_normalize_base_url_accepts_portal_page_urls(self):
        cases = {
            "http://172.16.2.14/srun_portal_pc?ac_id=1": "http://172.16.2.14",
            "http://10.11.22.1/srun_portal_pc?ac_id=4&theme=basic2": "http://10.11.22.1",
            "10.88.1.20/srun_portal_pc?ac_id=1": "http://10.88.1.20",
            "https://portal.example.edu/path/to/login": "https://portal.example.edu",
        }
        for raw, expected in cases.items():
            with self.subTest(raw=raw):
                self.assertEqual(school_presets.normalize_base_url(raw), expected)

    def test_builtin_presets_include_active_schools_but_hide_drafts(self):
        items = school_presets.list_presets()
        school_ids = {item["short_name"] for item in items}

        self.assertIn("lnut-hld", school_ids)
        self.assertIn("qdu", school_ids)
        self.assertNotIn("hsyu", school_ids)
        self.assertNotIn("lyu", school_ids)

        lnut = school_presets.get_preset("lnut-hld")
        self.assertEqual(lnut["defaults"]["base_url"], "http://10.11.22.1")
        self.assertEqual(lnut["defaults"]["operator_suffix"], "hcmcc")
        self.assertEqual(lnut["status"], "active")
        self.assertNotIn("verified", lnut)
        for operator in lnut["operators"]:
            self.assertNotIn("verified", operator)

    def test_remote_cache_overrides_builtin_presets(self):
        payload = {
            "schema_version": 1,
            "schools": [
                {
                    "id": "qdu",
                    "name": "青岛大学",
                    "status": "active",
                    "defaults": {"base_url": "http://10.0.0.1", "ac_id": "9"},
                }
            ],
        }
        with tempfile.TemporaryDirectory() as tmp:
            cache_path = os.path.join(tmp, "school_presets_cache.json")
            with open(cache_path, "w", encoding="utf-8") as handle:
                json.dump(payload, handle)
            with mock.patch.object(school_presets, "CACHE_PRESETS_FILE", cache_path):
                qdu = school_presets.get_preset("qdu")

        self.assertEqual(qdu["defaults"]["base_url"], "http://10.0.0.1")
        self.assertEqual(qdu["defaults"]["ac_id"], "9")

    def test_legacy_verified_preset_cache_is_accepted_but_not_exported(self):
        payload = {
            "schema_version": 1,
            "schools": [
                {
                    "id": "legacy",
                    "name": "旧缓存学校",
                    "verified": True,
                    "operators": [
                        {"id": "cmcc", "label": "中国移动", "verified": True}
                    ],
                    "defaults": {"base_url": "http://10.0.0.2"},
                }
            ],
        }

        items = school_presets.normalize_payload(payload)

        self.assertEqual(items[0]["status"], "active")
        self.assertNotIn("verified", items[0])
        self.assertNotIn("verified", items[0]["operators"][0])

    def test_presets_do_not_register_as_school_runtimes(self):
        for name in ["schools", "school_runtime"]:
            if name in sys.modules:
                importlib.reload(sys.modules[name])
        schools = importlib.import_module("schools")

        listed = {item["short_name"]: item for item in schools.list_schools()}
        self.assertIn("jxnu", listed)
        self.assertNotIn("lnut-hld", listed)
        self.assertNotIn("qdu", listed)


class SchoolPresetConfigTests(unittest.TestCase):
    def test_resolve_active_items_ignores_school_preset_defaults(self):
        cfg = {
            "school": "lnut-hld",
            "campus_accounts": [
                {
                    "id": "campus-1",
                    "user_id": "20260001",
                    "password": "secret",
                }
            ],
            "hotspot_profiles": [],
        }
        metadata = {
            "short_name": "lnut-hld",
            "no_suffix_operators": ["xn"],
            "defaults": {
                "base_url": "http://10.11.22.1/srun_portal_pc?ac_id=4&theme=basic2",
                "ac_id": "4",
                "access_mode": "wired",
                "operator": "cmcc",
                "operator_suffix": "hcmcc",
            },
        }
        with mock.patch("schools.get_school_metadata", return_value=metadata):
            resolved = config.resolve_active_items(cfg)

        self.assertEqual(resolved["base_url"], "http://172.17.1.2")
        self.assertEqual(resolved["ac_id"], "1")
        self.assertEqual(resolved["campus_access_mode"], "wifi")
        self.assertEqual(resolved["username"], "20260001@cucc")

    def test_resolve_active_items_still_normalizes_user_supplied_portal_origin(self):
        cfg = {
            "school": "jxnu",
            "campus_accounts": [
                {
                    "id": "campus-1",
                    "user_id": "u",
                    "operator": "xn",
                    "password": "p",
                    "base_url": "http://172.16.2.14/srun_portal_pc?ac_id=1",
                }
            ],
            "hotspot_profiles": [],
        }
        with mock.patch("schools.get_school_metadata", return_value={"no_suffix_operators": ["xn"]}):
            resolved = config.resolve_active_items(cfg)

        self.assertEqual(resolved["base_url"], "http://172.16.2.14")
        self.assertEqual(resolved["username"], "u")


if __name__ == "__main__":
    unittest.main()
