"""Explicit, versioned configuration backups; never import runtime/network state."""
import hashlib
import json
import math
import os
import re

import config

MAX_BYTES = 512 * 1024
FORMAT = "smart-srun-config"
ACCOUNT_KEYS = set("id label user_id password operator operator_suffix access_mode base_url ac_id ssid radio encryption key ap_selection bssid wired_iface auth_enabled n type enc info_prefix double_stack login_os login_name".split())
HOTSPOT_KEYS = set("id label ssid encryption key radio".split())


def _object(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise ValueError("备份包含重复字段")
        out[key] = value
    return out


def _decode(data):
    if len(data) > MAX_BYTES:
        raise ValueError("备份不能超过 512 KiB")
    try:
        value = json.loads(data.decode("utf-8"), object_pairs_hook=_object,
                           parse_constant=lambda _: (_ for _ in ()).throw(ValueError("无效数值")))
    except (UnicodeError, ValueError, RecursionError):
        raise ValueError("备份不是有效的 JSON 对象，或包含重复字段") from None
    def bounded(item, depth=0):
        if depth > 24:
            raise ValueError("备份嵌套层次过深")
        if isinstance(item, dict):
            for child in item.values():
                bounded(child, depth + 1)
        elif isinstance(item, list):
            for child in item:
                bounded(child, depth + 1)
    bounded(value)
    if not isinstance(value, dict):
        raise ValueError("备份必须是 JSON 对象")
    return value


def validate_config(value):
    allowed = config.GLOBAL_SCALAR_KEYS | config.POINTER_KEYS | config.LIST_KEYS | {"school_extra"}
    if not isinstance(value, dict) or set(value) - allowed:
        raise ValueError("备份包含未知配置字段")
    if not config.LIST_KEYS <= set(value):
        raise ValueError("备份缺少账号或热点列表")
    for key in config.GLOBAL_SCALAR_KEYS | config.POINTER_KEYS:
        if key in value and not isinstance(value[key], str):
            raise ValueError("1.x 备份的标量配置必须是字符串")
    boolean_keys = {"enabled", "multi_wan_enabled", "quiet_hours_enabled", "force_logout_in_quiet",
                    "failover_enabled", "backoff_enable", "hotspot_failback_enabled", "developer_mode"}
    for key in boolean_keys:
        if key in value and value[key] not in ("0", "1"):
            raise ValueError("开关配置必须为 0 或 1")
    for key in ("quiet_start", "quiet_end"):
        if key in value and not re.fullmatch(r"(?:[01][0-9]|2[0-3]):[0-5][0-9]", value[key]):
            raise ValueError("静默时段必须是 24 小时制 HH:MM")
    for key in config.GLOBAL_SCALAR_KEYS - boolean_keys:
        if key in value and len(value[key].encode("utf-8")) > 2048:
            raise ValueError("配置字段过长")
        if key in value and (key.startswith("backoff_") or key.endswith("_seconds") or key == "interval" or key.endswith("_attempts")):
            try:
                number = float(value[key])
            except ValueError:
                raise ValueError("时长或重试次数格式无效") from None
            if not math.isfinite(number) or number < 0 or number > 86400:
                raise ValueError("时长或重试次数超出范围")
    if value.get("connectivity_check_mode", "internet") not in ("internet", "portal", "ssid"):
        raise ValueError("联网检测模式无效")
    if value.get("log_level", "INFO") not in ("ALL", "DEBUG", "INFO", "WARN", "WARNING", "ERROR"):
        raise ValueError("日志级别无效")
    for kind, keys in (("campus_accounts", ACCOUNT_KEYS), ("hotspot_profiles", HOTSPOT_KEYS)):
        items = value[kind]
        if not isinstance(items, list) or len(items) > 32:
            raise ValueError("账号和热点列表各最多 32 项")
        ids = set()
        for item in items:
            if not isinstance(item, dict) or set(item) - keys or any(not isinstance(v, str) for v in item.values()):
                raise ValueError("账号或热点字段格式无效")
            identity = item.get("id", "")
            if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", identity) or identity in ids:
                raise ValueError("账号或热点 ID 无效或重复")
            ids.add(identity)
            if kind == "campus_accounts":
                if item.get("access_mode", "wifi") not in ("wifi", "wired"):
                    raise ValueError("账号接入方式无效")
                for key in ("auth_enabled", "double_stack"):
                    if key in item and item[key] not in ("", "0", "1"):
                        raise ValueError("账号开关配置格式无效")
            if any(len(v.encode("utf-8")) > 2048 for v in item.values()):
                raise ValueError("账号或热点字段过长")
        for key in (("active_campus_id", "default_campus_id") if kind == "campus_accounts" else ("active_hotspot_id", "default_hotspot_id")):
            if value.get(key) and value[key] not in ids:
                raise ValueError("当前或默认账号引用不存在")
    if not isinstance(value.get("school_extra", {}), dict):
        raise ValueError("学校扩展配置必须是对象")
    return value


def decode_backup(data):
    backup = _decode(data)
    if (set(backup) != {"format", "format_version", "config_schema", "config"}
            or backup["format"] != FORMAT or type(backup["format_version"]) is not int
            or backup["format_version"] != 1 or type(backup["config_schema"]) is not int
            or backup["config_schema"] != 1):
        raise ValueError("请选择 smart-srun 1.6.1 导出的配置备份")
    return validate_config(backup["config"])


def _current_bytes():
    try:
        with open(config.JSON_CONFIG_FILE, "rb") as handle:
            return handle.read(MAX_BYTES + 1)
    except FileNotFoundError:
        return b"{}"


def current_revision():
    return hashlib.sha256(_current_bytes()).hexdigest()


def export_backup():
    with config._exclusive_file_lock(config.JSON_CONFIG_FILE):
        raw = _decode(_current_bytes())
        if config._is_legacy_config(raw):
            raw = config._migrate_legacy_config(raw)
        raw = config._normalize_json_raw_config(raw)
        validate_config(raw)
        backup = {"format": FORMAT, "format_version": 1, "config_schema": 1, "config": raw}
        data = (json.dumps(backup, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        if len(data) > MAX_BYTES:
            raise ValueError("配置过大，无法导出")
        return data


def import_backup(data, expected_revision=None, check_only=False):
    expected_revision = expected_revision or current_revision()
    imported = decode_backup(data)
    # A backup may belong to another device/network. Importing credentials must
    # not immediately start authentication, quiet-hour logout or a Wi-Fi switch.
    imported = dict(imported, enabled="0")
    report = {"ok": True, "campus_accounts": len(imported["campus_accounts"]),
              "hotspot_profiles": len(imported["hotspot_profiles"]),
              "expected_revision": expected_revision,
              "message": "配置已校验；导入后自动守护保持关闭，确认账号和网口后再启用"}
    if check_only:
        return report
    with config._exclusive_file_lock(config.JSON_CONFIG_FILE):
        if current_revision() != expected_revision:
            raise ValueError("配置已被其他页面修改，请重新选择备份并预览")
        state = config.load_runtime_state()
        if (any(config._state_flag_enabled(state.get(key)) for key in
                ("manual_service_guard_active", "switch_service_guard_active"))
                or state.get("pending_action") or config.load_inflight_action().get("action")
                or config.load_json_file(config.ACTION_FILE).get("action")):
            raise ValueError("请等待认证或切网任务完成、返回校园网后再导入")
        config._atomic_save_json_unlocked(config.JSON_CONFIG_FILE, imported)
    report["message"] = "配置已导入；请确认账号和网口后再启用自动守护"
    return report


def write_export(path, data):
    # Do not overwrite an existing backup or follow a symlink.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())
