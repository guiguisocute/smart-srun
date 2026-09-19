local root = arg[1]
local h = dofile(root .. "/tests/lua/controller_harness.lua")(root)
local allowed = true
package.preload["luci.dispatcher"] = function()
    return { context = { authsession = "browser-session" }, test_post_security = function() return allowed end }
end

h.responses["detect.verify"] = { action_id = "verify1", state = "queued" }
h.form = { user_id = "student", password = "  exact secret  ", read_only = "0", candidates = "candidates", iface = "wan", access_mode = "wired" }
h.parsed = { candidates = { "" } }
h.controller.action_detect_operator()
local params = h.last_call("detect.verify").params
assert(params.password == "  exact secret  ", "password whitespace changed")
assert(params.session == "browser-session" and params.candidates[1] == "")
assert(#h.writes == 0 and #h.commands == 0, "draft used a file or shell")

h.reset()
h.form.read_only = "1"
h.responses["detect.identity"] = { action_id = "identity1", state = "queued" }
h.controller.action_detect_operator()
params = h.last_call("detect.identity").params
assert(params.password == nil and params.candidates == nil, "identity got credentials")

h.reset()
allowed = false
h.controller.action_detect_operator()
assert(#h.calls == 0, "CSRF rejection reached RPC")
allowed = true

h.reset()
h.form = { iface = "wan", idempotency_key = "refresh1" }
h.responses["presets.refresh"] = { action_id = "refresh1", state = "queued" }
h.controller.action_presets_refresh()
params = h.last_call("presets.refresh").params
assert(params.iface == "wan" and params.session == "browser-session" and params.base_url == nil)

h.reset()
h.form = { action = "status", action_id = "refresh1" }
h.responses["action.get"] = { id = "refresh1", state = "succeeded" }
h.responses["presets.list"] = function(p)
    if p.offset == 0 then return { public = {{short_name="first"}}, user = {}, revision=1, next_offset=1 } end
    assert(p.offset == 1)
    return { public = {}, user = {{short_name="last"}}, revision=1 }
end
h.controller.action_presets_refresh()
assert(#h.output.result.schools == 2 and h.output.result.schools[2].short_name == "last")
assert(h.last_call("presets.refresh") == nil, "polling repeated refresh")
assert(h.last_call("action.get").params.session == "browser-session")

h.responses["presets.list"] = function(p)
    return { public = {}, user = {}, revision = 1, next_offset = p.offset }
end
local result, err = h.bridge.presets()
assert(result == nil and err.code == "ProtocolInvalid", "non-advancing pagination accepted")
print("PASS discovery controller: exact password, read-only, CSRF, refresh receipt and pagination")
