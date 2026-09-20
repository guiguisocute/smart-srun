"""Page rendering stays local; only an explicit refresh starts a network job."""

import json
from pathlib import Path
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]


class PresetScheduleUITests(unittest.TestCase):
    def test_page_open_does_not_refresh_and_manual_refresh_can_retry(self):
        node = shutil.which("node")
        if not node:
            self.skipTest("node is not installed")
        script = r"""
const fs = require('fs'), vm = require('vm');
let source = fs.readFileSync(process.argv[1], 'utf8');
const end = source.lastIndexOf('})();');
source = source.slice(0, end) + `
window.initForTest = initTables;
postDiscovery = function(path, args, done) { window.requests.push(path); window.finish = done; };
` + source.slice(end);
const nodes = {};
['smart-campus-data', 'smart-hotspot-data', 'smart-school-preset-data', 'smart-user-preset-data'].forEach(id => {
  nodes[id] = {value: '[]', textContent: '[]'};
});
nodes['smart-presets-refresh'] = {};
nodes['smart-presets-refresh-result'] = {};
const context = {window: {requests: []}, document: {readyState: 'loading',
  addEventListener() {}, getElementById(id) { return nodes[id] || null; }}, JSON, Date};
vm.runInNewContext(source, context);
context.window.initForTest();
const opened = context.window.requests.length;
context.window.smartRefreshPresets();
context.window.smartRefreshPresets();
const pending = context.window.requests.length;
context.window.finish('failed');
const retryEnabled = !nodes['smart-presets-refresh'].disabled;
context.window.smartRefreshPresets();
context.window.finish(null, {ok: true, schools: [{short_name: 'demo'}]});
console.log(JSON.stringify({opened, pending, retryEnabled, requests: context.window.requests,
  schools: JSON.parse(nodes['smart-school-preset-data'].value),
  result: nodes['smart-presets-refresh-result'].textContent}));
"""
        result = subprocess.run(
            [node, "-e", script, str(ROOT / "root/www/luci-static/resources/smart_srun.js")],
            capture_output=True, text=True, encoding="utf-8", timeout=15,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(result.stdout)
        self.assertEqual(actual["opened"], 0)
        self.assertEqual(actual["pending"], 1)
        self.assertTrue(actual["retryEnabled"])
        self.assertEqual(actual["requests"], ["presets_refresh", "presets_refresh"])
        self.assertEqual(actual["schools"], [{"short_name": "demo"}])
        self.assertEqual(actual["result"], "学校预设已更新")

    def test_daily_selector_depends_on_checkbox(self):
        source = (ROOT / "root/usr/lib/lua/luci/model/cbi/smart_srun.lua").read_text(encoding="utf-8")
        self.assertIn('Flag, "preset_auto_update_enabled", "自动更新学校预设"', source)
        self.assertIn('preset_update_time:depends("preset_auto_update_enabled", "1")', source)
        self.assertIn('for hour = 0, 23 do', source)
        self.assertIn('string.format("%02d:00", hour)', source)
        self.assertIn('onclick="smartRefreshPresets()"', source)
