// ==UserScript==
// @name         Smart SRun Portal Diagnostic Capture
// @namespace    https://github.com/matthewlu070111/smart-srun
// @version      0.1.2
// @description  Capture redacted SRun 4000 portal diagnostics for smart-srun school onboarding.
// @author       smart-srun maintainers
// @match        http://172.16.2.14/*
// @run-at       document-start
// @grant        GM_setClipboard
// ==/UserScript==

(function () {
  "use strict";

  var SCRIPT_VERSION = "0.1.2";
  var MAX_EVENTS = 200;
  var STORAGE_KEY = "smart_srun_diagnostic_capture_v1";
  var STORAGE_TTL_MS = 2 * 60 * 60 * 1000;
  var ALPHA = "LVoJPiCN2R8G90yg+hmFHuacZ1OWMnrsSTXkYpUq/3dlbfKwv6xztjI7DeBE45QA";
  var events = [];
  var latestChallenge = "";
  var latestChallengeMeta = null;
  var pendingLoginParams = [];
  var summary = defaultSummary();
  var restoredFromStorage = false;
  var restoredStorageSavedAt = "";

  function defaultSummary() {
    return {
      challenge_seen: false,
      login_seen: false,
      info_decoded: false,
      checksum_observed_fields_match: null,
      last_login_error: "",
      observed_login_shape: null
    };
  }

  function nowIso() {
    return new Date().toISOString();
  }

  function isSrunUrl(url) {
    var text = String(url || "");
    return (
      text.indexOf("/cgi-bin/get_challenge") !== -1 ||
      text.indexOf("/cgi-bin/srun_portal") !== -1 ||
      text.indexOf("/cgi-bin/rad_user_info") !== -1 ||
      text.indexOf("/cgi-bin/rad_user_dm") !== -1 ||
      text.indexOf("/srun_portal_pc") !== -1
    );
  }

  function endpointName(url) {
    var text = String(url || "");
    if (text.indexOf("/cgi-bin/get_challenge") !== -1) {
      return "get_challenge";
    }
    if (text.indexOf("/cgi-bin/srun_portal") !== -1) {
      return "srun_portal";
    }
    if (text.indexOf("/cgi-bin/rad_user_info") !== -1) {
      return "rad_user_info";
    }
    if (text.indexOf("/cgi-bin/rad_user_dm") !== -1) {
      return "rad_user_dm";
    }
    if (text.indexOf("/srun_portal_pc") !== -1) {
      return "portal_page";
    }
    return "unknown";
  }

  function record(kind, payload) {
    events.push({
      time: nowIso(),
      kind: kind,
      data: payload || {}
    });
    if (events.length > MAX_EVENTS) {
      events.shift();
    }
    persistState();
    updatePanel();
  }

  function storageJsonParse(text) {
    try {
      return JSON.parse(text);
    } catch (exc) {
      return null;
    }
  }

  function persistState() {
    var payload;
    try {
      payload = {
        version: SCRIPT_VERSION,
        saved_at: nowIso(),
        saved_at_ms: Date.now(),
        origin: location.origin,
        latest_challenge: latestChallengeMeta,
        summary: summary,
        events: events
      };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
    } catch (exc) {
      // Storage may be disabled; the live in-page report still works.
    }
  }

  function clearPersistedState() {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch (exc) {
      // Ignore storage errors.
    }
  }

  function restorePersistedState() {
    var raw;
    var saved;
    var ageMs;

    try {
      raw = localStorage.getItem(STORAGE_KEY);
    } catch (exc) {
      raw = "";
    }
    if (!raw) {
      return;
    }

    saved = storageJsonParse(raw);
    if (!saved || saved.origin !== location.origin) {
      return;
    }
    ageMs = Date.now() - Number(saved.saved_at_ms || 0);
    if (ageMs < 0 || ageMs > STORAGE_TTL_MS) {
      clearPersistedState();
      return;
    }

    if (Object.prototype.toString.call(saved.events) === "[object Array]") {
      events = saved.events.slice(-MAX_EVENTS);
    }
    if (saved.summary && typeof saved.summary === "object") {
      summary = defaultSummary();
      summary.challenge_seen = !!saved.summary.challenge_seen;
      summary.login_seen = !!saved.summary.login_seen;
      summary.info_decoded = !!saved.summary.info_decoded;
      summary.checksum_observed_fields_match =
        saved.summary.checksum_observed_fields_match === null ||
        typeof saved.summary.checksum_observed_fields_match === "undefined" ?
          null :
          !!saved.summary.checksum_observed_fields_match;
      summary.last_login_error = String(saved.summary.last_login_error || "");
      summary.observed_login_shape = saved.summary.observed_login_shape || null;
    }
    if (saved.latest_challenge && typeof saved.latest_challenge === "object") {
      latestChallengeMeta = saved.latest_challenge;
    }
    if (events.length > 0) {
      restoredFromStorage = true;
      restoredStorageSavedAt = String(saved.saved_at || "");
    }
  }

  function resetCaptureState() {
    events = [];
    latestChallenge = "";
    latestChallengeMeta = null;
    pendingLoginParams = [];
    summary = defaultSummary();
    restoredFromStorage = false;
    restoredStorageSavedAt = "";
  }

  function parseUrl(url) {
    var a = document.createElement("a");
    a.href = String(url || "");
    return a;
  }

  function queryParams(url) {
    var a = parseUrl(url);
    var query = a.search ? a.search.substring(1) : "";
    var out = {};
    var parts;
    var idx;
    var pair;
    var key;
    var value;

    if (!query) {
      return out;
    }

    parts = query.split("&");
    for (idx = 0; idx < parts.length; idx += 1) {
      if (!parts[idx]) {
        continue;
      }
      pair = parts[idx].split("=");
      key = safeDecode(pair.shift() || "");
      value = safeDecode(pair.join("=") || "");
      out[key] = value;
    }
    return out;
  }

  function safeDecode(value) {
    try {
      return decodeURIComponent(String(value || "").replace(/\+/g, " "));
    } catch (exc) {
      return String(value || "");
    }
  }

  function pathOnly(url) {
    var a = parseUrl(url);
    return a.protocol + "//" + a.host + a.pathname;
  }

  function maskAccount(value) {
    var text = String(value || "").trim();
    var parts;
    var local;
    var suffix;

    if (!text) {
      return "";
    }

    parts = text.split("@");
    local = parts[0];
    suffix = parts.length > 1 ? "@" + parts.slice(1).join("@") : "";

    if (local.length <= 2) {
      return "**" + suffix;
    }
    if (local.length <= 5) {
      return local.charAt(0) + "***" + suffix;
    }
    return local.charAt(0) + "***" + local.slice(-2) + suffix;
  }

  function secretShape(value) {
    var text = String(value || "");
    if (!text) {
      return "(empty)";
    }
    if (text.indexOf("{MD5}") === 0) {
      return "{MD5}<redacted len=" + Math.max(text.length - 5, 0) + ">";
    }
    return "<redacted len=" + text.length + ">";
  }

  function redactToken(value) {
    var text = String(value || "");
    if (!text) {
      return "";
    }
    return "<redacted len=" + text.length + " sha1=" + sha1(text).slice(0, 12) + ">";
  }

  function redactInfo(value) {
    var text = String(value || "");
    if (!text) {
      return "";
    }
    if (text.indexOf("{SRBX1}") === 0) {
      return "{SRBX1}<redacted chars=" + Math.max(text.length - 7, 0) +
        " sha1=" + sha1(text).slice(0, 12) + ">";
    }
    return "<redacted chars=" + text.length + " sha1=" + sha1(text).slice(0, 12) + ">";
  }

  function normalizePasswordForChecksum(value) {
    var text = String(value || "");
    if (text.indexOf("{MD5}") === 0) {
      return text.substring(5);
    }
    return text;
  }

  function safeParams(params) {
    var out = {};
    var key;
    for (key in params) {
      if (!Object.prototype.hasOwnProperty.call(params, key)) {
        continue;
      }
      if (key === "username" || key === "user_name" || key === "uid") {
        out[key] = maskAccount(params[key]) || "(empty)";
      } else if (key === "password" || key === "pwd" || key === "pass") {
        out[key] = secretShape(params[key]);
      } else if (key === "info") {
        out[key] = redactInfo(params[key]);
      } else if (key === "token" || key === "challenge") {
        out[key] = redactToken(params[key]);
      } else {
        out[key] = params[key];
      }
    }
    return out;
  }

  function sanitizeResponse(value) {
    var out;
    var key;
    var lower;

    if (value === null || value === undefined) {
      return value;
    }
    if (typeof value === "string") {
      if (value.length > 500) {
        return value.slice(0, 500) + "...<truncated>";
      }
      return value;
    }
    if (typeof value !== "object") {
      return value;
    }
    if (Object.prototype.toString.call(value) === "[object Array]") {
      return value.slice(0, 20).map(sanitizeResponse);
    }

    out = {};
    for (key in value) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) {
        continue;
      }
      lower = String(key).toLowerCase();
      if (lower.indexOf("password") !== -1 || lower === "pwd" || lower === "pass") {
        out[key] = secretShape(value[key]);
      } else if (lower === "challenge" || lower.indexOf("token") !== -1) {
        out[key] = redactToken(value[key]);
      } else if (lower === "username" || lower === "user_name" || lower === "uid") {
        out[key] = maskAccount(value[key]) || "(empty)";
      } else if (
          lower === "real_name" || lower === "realname" ||
          lower.indexOf("phone") !== -1 || lower.indexOf("mobile") !== -1 ||
          lower.indexOf("email") !== -1 || lower.indexOf("mac") !== -1 ||
          lower.indexOf("session") !== -1) {
        out[key] = "<redacted len=" + String(value[key] || "").length + ">";
      } else {
        out[key] = sanitizeResponse(value[key]);
      }
    }
    return out;
  }

  function parseJsonpOrJson(text) {
    var body = String(text || "").trim();
    var match;
    if (!body) {
      return null;
    }
    match = body.match(/^[^(]*\(([\s\S]*)\)\s*;?\s*$/);
    if (match) {
      body = match[1];
    }
    try {
      return JSON.parse(body);
    } catch (exc) {
      return null;
    }
  }

  function handleResponse(endpoint, url, payload, transport) {
    var challenge;
    var params = queryParams(url);

    if (payload && typeof payload === "object") {
      challenge = payload.challenge || payload.token;
      if (challenge) {
        latestChallenge = String(challenge);
        latestChallengeMeta = {
          endpoint: endpoint,
          transport: transport,
          received_at: nowIso(),
          token: redactToken(latestChallenge)
        };
        summary.challenge_seen = true;
        record("challenge_captured", latestChallengeMeta);
        replayPendingLoginParams();
      }

      if (endpoint === "srun_portal") {
        if (payload.error_msg || payload.error || payload.res) {
          summary.last_login_error = String(
            payload.error_msg || payload.error || payload.res || ""
          );
        }
      }
    }

    record("response", {
      endpoint: endpoint,
      transport: transport,
      url: pathOnly(url),
      callback: params.callback || "",
      body: sanitizeResponse(payload)
    });
  }

  function handleRequest(url, transport, method) {
    var endpoint;
    var params;

    if (!isSrunUrl(url)) {
      return;
    }

    endpoint = endpointName(url);
    params = queryParams(url);
    record("request", {
      endpoint: endpoint,
      transport: transport,
      method: method || "GET",
      url: pathOnly(url),
      params: safeParams(params)
    });

    if (endpoint === "srun_portal" && String(params.action || "") === "login") {
      summary.login_seen = true;
      inspectLoginParams(params, transport);
    }

    if (params.callback) {
      installJsonpCallbackWrapper(params.callback, endpoint, url);
    }
  }

  function inspectLoginParams(params, transport) {
    var shape = {
      transport: transport,
      top_level_username: params.username ? "present" : "empty",
      top_level_password: params.password ? secretShape(params.password) : "empty",
      ac_id: params.ac_id || "",
      ip: params.ip || "",
      n: params.n || "",
      type: params.type || "",
      os: params.os || "",
      name: params.name || "",
      info: params.info ? redactInfo(params.info) : ""
    };

    summary.observed_login_shape = shape;
    record("login_shape", shape);

    if (!latestChallenge) {
      pendingLoginParams.push(params);
      record("info_decode_waiting_for_challenge", {
        reason: "login request seen before challenge callback was captured"
      });
      return;
    }

    decodeAndInspectInfo(params, latestChallenge);
  }

  function replayPendingLoginParams() {
    var queued = pendingLoginParams.slice();
    var idx;
    pendingLoginParams = [];
    for (idx = 0; idx < queued.length; idx += 1) {
      decodeAndInspectInfo(queued[idx], latestChallenge);
    }
  }

  function decodeAndInspectInfo(params, token) {
    var info = String(params.info || "");
    var encoded;
    var binary;
    var jsonText;
    var decoded;
    var checksum;
    var observedPassword;
    var match;

    if (info.indexOf("{SRBX1}") !== 0) {
      record("info_decode_skipped", {
        reason: "missing {SRBX1} prefix",
        info: redactInfo(info)
      });
      return;
    }

    try {
      encoded = info.substring(7);
      binary = srunBase64Decode(encoded, ALPHA);
      jsonText = xxteaDecrypt(binary, token);
      decoded = JSON.parse(jsonText);
    } catch (exc) {
      record("info_decode_failed", {
        error: String(exc && exc.message ? exc.message : exc),
        info: redactInfo(info),
        challenge: redactToken(token)
      });
      return;
    }

    observedPassword = normalizePasswordForChecksum(params.password || "");
    checksum = sha1(
      token + String(params.username || "") +
      token + observedPassword +
      token + String(params.ac_id || "") +
      token + String(params.ip || "") +
      token + String(params.n || "") +
      token + String(params.type || "") +
      token + info
    );
    match = checksum.toLowerCase() === String(params.chksum || "").toLowerCase();
    summary.info_decoded = true;
    summary.checksum_observed_fields_match = match;

    record("info_decoded", {
      decoded: {
        username: maskAccount(decoded.username),
        password: decoded.password ? "<present len=" + String(decoded.password).length + ">" : "(empty)",
        ip: decoded.ip || "",
        acid: decoded.acid || "",
        enc_ver: decoded.enc_ver || "",
        keys: Object.keys(decoded).sort()
      },
      top_level_username: params.username ? "present" : "empty",
      top_level_password: params.password ? secretShape(params.password) : "empty",
      checksum_observed_fields_match: match,
      checksum_rule_checked: "sha1(token + top-level username + token + top-level password-without-{MD5} + token + ac_id + token + ip + token + n + token + type + token + info)",
      challenge: redactToken(token),
      info: redactInfo(info)
    });
  }

  function srunBase64Decode(input, alpha) {
    var clean = String(input || "").replace(/\s/g, "");
    var out = "";
    var idx;
    var c1;
    var c2;
    var c3;
    var c4;
    var b10;

    if (clean.length % 4 !== 0) {
      throw new Error("invalid custom base64 length");
    }

    for (idx = 0; idx < clean.length; idx += 4) {
      c1 = alpha.indexOf(clean.charAt(idx));
      c2 = alpha.indexOf(clean.charAt(idx + 1));
      c3 = clean.charAt(idx + 2) === "=" ? -1 : alpha.indexOf(clean.charAt(idx + 2));
      c4 = clean.charAt(idx + 3) === "=" ? -1 : alpha.indexOf(clean.charAt(idx + 3));

      if (c1 < 0 || c2 < 0 || (c3 < 0 && clean.charAt(idx + 2) !== "=") ||
          (c4 < 0 && clean.charAt(idx + 3) !== "=")) {
        throw new Error("invalid custom base64 character");
      }

      b10 = (c1 << 18) | (c2 << 12) | ((c3 < 0 ? 0 : c3) << 6) | (c4 < 0 ? 0 : c4);
      out += String.fromCharCode((b10 >> 16) & 255);
      if (clean.charAt(idx + 2) !== "=") {
        out += String.fromCharCode((b10 >> 8) & 255);
      }
      if (clean.charAt(idx + 3) !== "=") {
        out += String.fromCharCode(b10 & 255);
      }
    }
    return out;
  }

  function ordat(msg, idx) {
    return msg.length > idx ? msg.charCodeAt(idx) : 0;
  }

  function sencode(msg, includeLength) {
    var length = msg.length;
    var out = [];
    var idx;
    for (idx = 0; idx < length; idx += 4) {
      out.push(
        (ordat(msg, idx) |
          (ordat(msg, idx + 1) << 8) |
          (ordat(msg, idx + 2) << 16) |
          (ordat(msg, idx + 3) << 24)) >>> 0
      );
    }
    if (includeLength) {
      out.push(length);
    }
    return out;
  }

  function lencode(words, includeLength) {
    var length = words.length;
    var ll = (length - 1) << 2;
    var idx;
    var m;
    var out = "";

    if (includeLength) {
      m = words[length - 1];
      if (m < ll - 3 || m > ll) {
        return null;
      }
      ll = m;
    }

    for (idx = 0; idx < length; idx += 1) {
      out += String.fromCharCode(words[idx] & 255);
      out += String.fromCharCode((words[idx] >>> 8) & 255);
      out += String.fromCharCode((words[idx] >>> 16) & 255);
      out += String.fromCharCode((words[idx] >>> 24) & 255);
    }
    return includeLength ? out.substring(0, ll) : out;
  }

  function xxteaDecrypt(msg, key) {
    var v = sencode(msg, false);
    var k = sencode(key, false);
    var n = v.length - 1;
    var z;
    var y;
    var q;
    var sum;
    var e;
    var p;
    var mx;
    var DELTA = 0x9E3779B9 >>> 0;

    if (!msg) {
      return "";
    }
    while (k.length < 4) {
      k.push(0);
    }
    if (n < 1) {
      return lencode(v, true);
    }

    y = v[0];
    q = Math.floor(6 + 52 / (n + 1));
    sum = (q * DELTA) >>> 0;
    while (sum !== 0) {
      e = (sum >>> 2) & 3;
      for (p = n; p > 0; p -= 1) {
        z = v[p - 1];
        mx = (((z >>> 5) ^ (y << 2)) + (((y >>> 3) ^ (z << 4)) ^ (sum ^ y)) +
          (k[(p & 3) ^ e] ^ z)) >>> 0;
        y = v[p] = (v[p] - mx) >>> 0;
      }
      z = v[n];
      mx = (((z >>> 5) ^ (y << 2)) + (((y >>> 3) ^ (z << 4)) ^ (sum ^ y)) +
        (k[(p & 3) ^ e] ^ z)) >>> 0;
      y = v[0] = (v[0] - mx) >>> 0;
      sum = (sum - DELTA) >>> 0;
    }
    return lencode(v, true);
  }

  function installJsonpCallbackWrapper(callbackName, endpoint, url) {
    var current;
    if (!callbackName || typeof callbackName !== "string") {
      return;
    }
    if (window[callbackName] && window[callbackName].__srunDiagnosticWrapped) {
      return;
    }

    current = window[callbackName];
    if (typeof current === "function") {
      window[callbackName] = wrapJsonpCallback(current, callbackName, endpoint, url);
      return;
    }

    try {
      Object.defineProperty(window, callbackName, {
        configurable: true,
        enumerable: true,
        get: function () {
          return current;
        },
        set: function (fn) {
          current = typeof fn === "function" ?
            wrapJsonpCallback(fn, callbackName, endpoint, url) :
            fn;
        }
      });
    } catch (exc) {
      // Some pages lock callback properties. Missing the response is acceptable;
      // request metadata and resource timing will still be captured.
    }
  }

  function wrapJsonpCallback(fn, callbackName, endpoint, url) {
    var wrapped = function () {
      if (arguments.length > 0) {
        handleResponse(endpoint, url, arguments[0], "jsonp");
      }
      return fn.apply(this, arguments);
    };
    wrapped.__srunDiagnosticWrapped = true;
    wrapped.__srunDiagnosticCallback = callbackName;
    return wrapped;
  }

  function patchXhr() {
    var proto = window.XMLHttpRequest && window.XMLHttpRequest.prototype;
    var originalOpen;
    var originalSend;
    if (!proto) {
      return;
    }
    originalOpen = proto.open;
    originalSend = proto.send;

    proto.open = function (method, url) {
      this.__srunDiagnosticUrl = String(url || "");
      this.__srunDiagnosticMethod = String(method || "GET");
      return originalOpen.apply(this, arguments);
    };

    proto.send = function () {
      var xhr = this;
      var url = xhr.__srunDiagnosticUrl || "";
      if (isSrunUrl(url)) {
        handleRequest(url, "xhr", xhr.__srunDiagnosticMethod || "GET");
        xhr.addEventListener("loadend", function () {
          var parsed = null;
          try {
            parsed = parseJsonpOrJson(xhr.responseText);
          } catch (exc) {
            parsed = null;
          }
          handleResponse(endpointName(url), url, parsed || {
            status: xhr.status,
            responseText: String(xhr.responseText || "").slice(0, 500)
          }, "xhr");
        });
      }
      return originalSend.apply(this, arguments);
    };
  }

  function patchFetch() {
    var originalFetch = window.fetch;
    if (!originalFetch) {
      return;
    }
    window.fetch = function (input, init) {
      var url = typeof input === "string" ? input : (input && input.url) || "";
      var method = (init && init.method) || (input && input.method) || "GET";
      var promise;
      if (isSrunUrl(url)) {
        handleRequest(url, "fetch", method);
      }
      promise = originalFetch.apply(this, arguments);
      if (isSrunUrl(url)) {
        promise.then(function (response) {
          try {
            response.clone().text().then(function (text) {
              handleResponse(endpointName(url), url, parseJsonpOrJson(text) || {
                status: response.status,
                responseText: String(text || "").slice(0, 500)
              }, "fetch");
            });
          } catch (exc) {
            record("response_read_failed", {
              endpoint: endpointName(url),
              transport: "fetch",
              error: String(exc && exc.message ? exc.message : exc)
            });
          }
        });
      }
      return promise;
    };
  }

  function patchScriptInsertion() {
    var originalAppendChild = Node.prototype.appendChild;
    var originalInsertBefore = Node.prototype.insertBefore;
    var srcDescriptor = Object.getOwnPropertyDescriptor(HTMLScriptElement.prototype, "src");
    var originalSetAttribute = HTMLScriptElement.prototype.setAttribute;

    function inspectNode(node) {
      var src = node && node.tagName && String(node.tagName).toLowerCase() === "script" ?
        node.src || node.getAttribute("src") || "" :
        "";
      if (src && isSrunUrl(src)) {
        handleRequest(src, "jsonp-script", "GET");
      }
    }

    Node.prototype.appendChild = function (node) {
      inspectNode(node);
      return originalAppendChild.apply(this, arguments);
    };

    Node.prototype.insertBefore = function (node) {
      inspectNode(node);
      return originalInsertBefore.apply(this, arguments);
    };

    HTMLScriptElement.prototype.setAttribute = function (name, value) {
      if (String(name || "").toLowerCase() === "src" && isSrunUrl(value)) {
        handleRequest(String(value || ""), "jsonp-script", "GET");
      }
      return originalSetAttribute.apply(this, arguments);
    };

    if (srcDescriptor && srcDescriptor.set && srcDescriptor.get) {
      try {
        Object.defineProperty(HTMLScriptElement.prototype, "src", {
          configurable: true,
          enumerable: srcDescriptor.enumerable,
          get: function () {
            return srcDescriptor.get.call(this);
          },
          set: function (value) {
            if (isSrunUrl(value)) {
              handleRequest(String(value || ""), "jsonp-script", "GET");
            }
            return srcDescriptor.set.call(this, value);
          }
        });
      } catch (exc) {
        // Older browsers may reject prototype descriptor changes.
      }
    }
  }

  function watchResourceTiming() {
    function inspectEntries(entries) {
      var idx;
      var entry;
      for (idx = 0; idx < entries.length; idx += 1) {
        entry = entries[idx];
        if (entry && isSrunUrl(entry.name)) {
          record("resource_timing", {
            endpoint: endpointName(entry.name),
            url: pathOnly(entry.name),
            initiatorType: entry.initiatorType || "",
            duration_ms: Math.round(entry.duration || 0),
            transferSize: entry.transferSize || 0
          });
        }
      }
    }

    if (window.PerformanceObserver) {
      try {
        new PerformanceObserver(function (list) {
          inspectEntries(list.getEntries());
        }).observe({ entryTypes: ["resource"] });
      } catch (exc) {
        // Ignore unsupported observers.
      }
    }

    setTimeout(function () {
      if (window.performance && performance.getEntriesByType) {
        inspectEntries(performance.getEntriesByType("resource"));
      }
    }, 3000);
  }

  var panel = null;
  var statusNode = null;

  function ensurePanel() {
    var copyButton;
    var downloadButton;
    var clearButton;

    if (panel || !document.body) {
      return;
    }

    panel = document.createElement("div");
    panel.style.cssText = [
      "position:fixed",
      "right:12px",
      "bottom:12px",
      "z-index:2147483647",
      "background:#111827",
      "color:#e5e7eb",
      "font:12px/1.4 -apple-system,BlinkMacSystemFont,Segoe UI,sans-serif",
      "border:1px solid #374151",
      "border-radius:6px",
      "box-shadow:0 8px 24px rgba(0,0,0,.25)",
      "padding:10px",
      "max-width:340px"
    ].join(";");

    panel.innerHTML =
      '<div style="font-weight:700;margin-bottom:6px;">Smart SRun 诊断采集</div>' +
      '<div data-role="status" style="margin-bottom:8px;color:#cbd5e1;">等待手动登录...</div>';

    statusNode = panel.querySelector('[data-role="status"]');

    copyButton = makeButton("复制诊断日志");
    copyButton.onclick = copyReport;
    downloadButton = makeButton("下载 JSON");
    downloadButton.onclick = downloadReport;
    clearButton = makeButton("清空");
    clearButton.onclick = function () {
      resetCaptureState();
      clearPersistedState();
      updatePanel();
    };

    panel.appendChild(copyButton);
    panel.appendChild(downloadButton);
    panel.appendChild(clearButton);
    document.body.appendChild(panel);
    updatePanel();
  }

  function makeButton(text) {
    var button = document.createElement("button");
    button.type = "button";
    button.textContent = text;
    button.style.cssText = [
      "margin-right:6px",
      "padding:4px 8px",
      "border:1px solid #4b5563",
      "border-radius:4px",
      "background:#1f2937",
      "color:#f9fafb",
      "cursor:pointer"
    ].join(";");
    return button;
  }

  function updatePanel() {
    if (!statusNode) {
      return;
    }
    statusNode.textContent =
      (restoredFromStorage ? "已恢复上次捕获 | " : "") +
      "事件 " + events.length +
      " | challenge " + (summary.challenge_seen ? "已捕获" : "未捕获") +
      " | login " + (summary.login_seen ? "已捕获" : "未捕获") +
      " | info " + (summary.info_decoded ? "已本地解码" : "未解码");
  }

  function buildReport() {
    return JSON.stringify({
      tool: "smart-srun portal diagnostic capture",
      version: SCRIPT_VERSION,
      generated_at: nowIso(),
      redaction: "username/password/challenge/info are masked or hashed; raw secrets are intentionally not exported.",
      page: {
        origin: location.origin,
        pathname: location.pathname,
        userAgent: navigator.userAgent
      },
      restored_from_storage: restoredFromStorage ? {
        saved_at: restoredStorageSavedAt
      } : null,
      latest_challenge: latestChallengeMeta,
      summary: summary,
      events: events
    }, null, 2);
  }

  function copyReport() {
    var text = buildReport();
    try {
      if (typeof GM_setClipboard === "function") {
        GM_setClipboard(text, "text");
        record("ui", { action: "copy_report", result: "ok" });
        return;
      }
    } catch (exc) {
      // Fall through to the browser clipboard API.
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        record("ui", { action: "copy_report", result: "ok" });
      }, function (err) {
        record("ui", { action: "copy_report", result: "failed", error: String(err) });
      });
    } else {
      window.prompt("复制下面的诊断日志", text);
    }
  }

  function downloadReport() {
    var blob = new Blob([buildReport()], { type: "application/json;charset=utf-8" });
    var a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "smart-srun-diagnostic-" + Date.now() + ".json";
    document.body.appendChild(a);
    a.click();
    setTimeout(function () {
      URL.revokeObjectURL(a.href);
      if (a.parentNode) {
        a.parentNode.removeChild(a);
      }
    }, 1000);
    record("ui", { action: "download_report", result: "ok" });
  }

  function sha1(message) {
    function rotl(n, s) {
      return (n << s) | (n >>> (32 - s));
    }

    function toUtf8Bytes(str) {
      var unescaped = unescape(encodeURIComponent(str));
      var bytes = [];
      var idx;
      for (idx = 0; idx < unescaped.length; idx += 1) {
        bytes.push(unescaped.charCodeAt(idx));
      }
      return bytes;
    }

    var bytes = toUtf8Bytes(String(message || ""));
    var bitLen = bytes.length * 8;
    var words = [];
    var h0 = 0x67452301;
    var h1 = 0xEFCDAB89;
    var h2 = 0x98BADCFE;
    var h3 = 0x10325476;
    var h4 = 0xC3D2E1F0;
    var highLen;
    var lowLen;
    var i;
    var j;
    var chunk;
    var w;
    var a;
    var b;
    var c;
    var d;
    var e;
    var f;
    var k;
    var temp;

    bytes.push(0x80);
    while ((bytes.length % 64) !== 56) {
      bytes.push(0);
    }
    highLen = Math.floor(bitLen / 0x100000000);
    lowLen = bitLen >>> 0;
    bytes.push((highLen >>> 24) & 255);
    bytes.push((highLen >>> 16) & 255);
    bytes.push((highLen >>> 8) & 255);
    bytes.push(highLen & 255);
    bytes.push((lowLen >>> 24) & 255);
    bytes.push((lowLen >>> 16) & 255);
    bytes.push((lowLen >>> 8) & 255);
    bytes.push(lowLen & 255);

    for (i = 0; i < bytes.length; i += 4) {
      words.push(
        (bytes[i] << 24) |
        (bytes[i + 1] << 16) |
        (bytes[i + 2] << 8) |
        bytes[i + 3]
      );
    }

    for (chunk = 0; chunk < words.length; chunk += 16) {
      w = [];
      for (i = 0; i < 16; i += 1) {
        w[i] = words[chunk + i] | 0;
      }
      for (i = 16; i < 80; i += 1) {
        w[i] = rotl(w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1) | 0;
      }

      a = h0;
      b = h1;
      c = h2;
      d = h3;
      e = h4;

      for (i = 0; i < 80; i += 1) {
        if (i < 20) {
          f = (b & c) | ((~b) & d);
          k = 0x5A827999;
        } else if (i < 40) {
          f = b ^ c ^ d;
          k = 0x6ED9EBA1;
        } else if (i < 60) {
          f = (b & c) | (b & d) | (c & d);
          k = 0x8F1BBCDC;
        } else {
          f = b ^ c ^ d;
          k = 0xCA62C1D6;
        }
        temp = (rotl(a, 5) + f + e + k + w[i]) | 0;
        e = d;
        d = c;
        c = rotl(b, 30) | 0;
        b = a;
        a = temp;
      }

      h0 = (h0 + a) | 0;
      h1 = (h1 + b) | 0;
      h2 = (h2 + c) | 0;
      h3 = (h3 + d) | 0;
      h4 = (h4 + e) | 0;
    }

    function hex(num) {
      var s = (num >>> 0).toString(16);
      while (s.length < 8) {
        s = "0" + s;
      }
      return s;
    }

    return hex(h0) + hex(h1) + hex(h2) + hex(h3) + hex(h4);
  }

  restorePersistedState();
  window.addEventListener("beforeunload", persistState);
  window.addEventListener("pagehide", persistState);
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") {
      persistState();
    }
  });

  patchXhr();
  patchFetch();
  patchScriptInsertion();
  watchResourceTiming();
  record("script_loaded", {
    version: SCRIPT_VERSION,
    page: location.origin + location.pathname
  });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", ensurePanel);
  } else {
    ensurePanel();
  }
})();
