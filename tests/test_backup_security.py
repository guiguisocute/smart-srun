"""Mutating HTTP handlers refuse failed CSRF checks before any side effect."""
import shutil
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

class BackupSecurityTests(unittest.TestCase):
    def test_controller_checks_security_before_reading_body_or_running_commands(self):
        lua = shutil.which("lua")
        if not lua:
            self.skipTest("lua unavailable")
        script = r"""
local checked = 0
local function forbidden() error("side effect before CSRF check") end
package.preload["luci.dispatcher"] = function() return {test_post_security=function() checked=checked+1; return false end} end
package.preload["luci.http"] = function() return {formvalue=forbidden,write=forbidden} end
package.preload["luci.jsonc"] = function() return {parse=forbidden,stringify=forbidden} end
package.preload["luci.sys"] = function() return {exec=forbidden,call=forbidden} end
package.preload["luci.util"] = function() return {} end
package.preload["nixio.fs"] = function() return {readfile=forbidden,writefile=forbidden} end
package.preload["luci.smart_srun.schema"] = function() return {POINTER_KEYS={},LIST_KEYS={},global_scalar_key_set=function() return {} end} end
dofile(arg[1])
local c=package.loaded["luci.controller.smart_srun"]
for _,name in ipairs({"action_config_export","action_config_import","action_enqueue","action_update_start","action_presets_refresh","action_user_presets_set","action_detect_operator","action_setup_wifi","action_log_clear"}) do
 local before=checked; c[name](); assert(checked==before+1,name)
end
"""
        result = subprocess.run([lua, "-", str(ROOT/"root/usr/lib/lua/luci/controller/smart_srun.lua")], input=script, text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_private_lua_writer_refuses_short_write_and_rename_failure(self):
        lua = shutil.which("lua")
        if not lua:
            self.skipTest("lua unavailable")
        script = r"""
local files = {config="old"}
local short, rename_fail = false, false
package.preload["luci.jsonc"] = function() return {parse=function() return {} end,stringify=function() return "secret bytes" end} end
package.preload["nixio.fs"] = function() return {readfile=function() return nil end,access=function() return true end,unlink=function(p) files[p]=nil end} end
package.preload["nixio"] = function() return {
 getpid=function() return 99 end,
 open_flags=function(...) return table.concat({...},",") end,
 open=function(path,flags,mode)
  assert(flags=="wronly,creat,excl" and mode=="600")
  assert(not files[path]); files[path]=""
  return {write=function(_,data) files[path]=data; return short and 1 or #data end,close=function() end}
 end
} end
os.rename=function(from,to) if rename_fail then return nil end; files[to]=files[from];files[from]=nil;return true end
local schema=dofile(arg[1])
short=true; assert(not pcall(schema.write_private_json,"config",{})); assert(files.config=="old" and files["config.tmp.99"]==nil)
short=false;rename_fail=true;assert(not pcall(schema.write_private_json,"config",{}));assert(files.config=="old" and files["config.tmp.99"]==nil)
rename_fail=false;schema.write_private_json("config",{});assert(files.config=="secret bytes\n")
"""
        result = subprocess.run([lua, "-", str(ROOT/"root/usr/lib/lua/luci/smart_srun/schema.lua")], input=script, text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

if __name__ == "__main__":
    unittest.main()
