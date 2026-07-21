# 南昌航空大学（前湖）校园网认证备忘

**学校预设 ID：** `nchu`  
**状态：** 2026-07-20 在 `10.0.0.2` 上实测 **Wi‑Fi 登录/登出可用**（账号后缀修正后）。  
**关联 Issue：** [#22](https://github.com/matthewlu070111/smart-srun/issues/22)

本文只记录**已用上下文核实过**的字段；未验证项单独标明。有线 PPPoE/拨号不在本次实测范围内。

---

## 1. 环境默认值

| 字段 | 值 | 依据 |
|------|-----|------|
| `base_url` | `http://10.1.88.4` | 油猴诊断、门户 HTML、设备 `rad_user_info` |
| `ac_id` | `1` | 油猴 / 门户 URL `srun_portal_pc?ac_id=1&theme=pro` |
| `ssid` | `NCHU_Wireless` | 设备关联成功日志 |
| `access_mode` | `wifi` | 本次实测为无线 STA |

门户标题：网络准入认证（深澜 theme **pro**）。

### 网络侧观察（Wi‑Fi 已关联时）

| 项 | 观察值 |
|----|--------|
| STA 地址示例 | `10.83.x.x/16`（实测出现过 `10.83.2.21`、`10.83.1.186`） |
| 默认网关示例 | `10.83.0.1` |
| 认证网关 | `10.1.88.4` 可 ping、HTTP 可达 |
| 未连校园网时 | 网关不可达 / 空响应 / Operation not permitted（预期） |

---

## 2. 账号与运营商后缀（关键）

### 已验证

| 项 | 值 |
|----|-----|
| 在线用户名形态 | **`{学号}@stu.nchu.edu.cn`** |
| 应配置的 `operator_suffix` | **`stu.nchu.edu.cn`** |
| 页面相关文案 | **学生用户**（油猴 DOM / 导出 label） |
| 插件登录 | `error_code=ok`，约 300ms 级 |
| 插件登出 | 成功 → `rad_user_info` = `not_online_error`，再登录可恢复 |

### 错误写法（勿再用）

| 错误 suffix | 拼出的用户名 | 来源 |
|-------------|--------------|------|
| `1-@stu.nchu.edu.cn` | `学号@1-@stu.nchu.edu.cn` | 油猴旧逻辑 / 深澜产品序号拼进 domain |

根因简述：门户 option/domain 常为 **`{产品序号}-@{真实域名}`**（例 `1-@stu.nchu.edu.cn`）。  
完整 username 若为 `学号@1-@stu.nchu.edu.cn`，用「第一个 `@`」切开会把后缀误当成整段 `1-@…`。

采集脚本侧修复说明见独立仓库：

`smart_srun_school_preset_capture` → `doc/deeplan-operator-suffix.md`（v0.2.1+）。

### 运营商标签说明

南航**不是**简单的「移动 / 电信 / 联通」三选一：

- 已确认可用：suffix = `stu.nchu.edu.cn`，label 宜为 **学生用户**（或保留域名）  
- 油猴曾出现另一文案「校园网」与同一域名相关；**是否存在无 `@` 的纯学号套餐未在本次严格区分验证**  
- Issue #22 提到「ISP 中国电信」是接入侧描述，**不等于** suffix 必须是 `ctcc`  
- 预设里旧的 `{"suffix":"??","label":"中国电信"}` 表示「可能还有电信类套餐但后缀未证实」；在未抓到真 suffix 前不要改成臆测 token

---

## 3. 登录形态 `observed_login_shape`

| 字段 | 值 | 说明 |
|------|-----|------|
| `n` | `200` | 油猴 + 账号高级字段 |
| `type` | `1` | 同上 |
| `enc` | `srun_bx1` | 同上 |
| `info_prefix` | `SRBX1` | 同上 |
| `double_stack` | `0` | 同上 |
| `os` / `name` | 随客户端 | 油猴 Windows 抓包曾为 `Windows 10` / `Windows`；旧预设为 Mac |

账号级高级字段优先于全局 `n/type/enc`。

---

## 4. 插件配置注意（设备侧）

1. **活跃校园账号**使用南航条目时：  
   - `base_url` / `ac_id` / `ssid` / `operator_suffix` 按上表  
   - `user_id` 仅学号，**不要**自带 `@…`，也**不要**尾随换行  
2. 配置项 `school: "nchu"` 若设备**没有**名为 `nchu` 的本地 school 运行时模块，会告警  
   `unknown school in config, using built-in default`  
   一般仍可用账号级 `base_url` 走默认 SRun 流程；消告警可把 school 设回内置默认（如 `jxnu`），靠账号字段区分学校。  
3. `connectivity_check_mode=internet` 时，登录成功后可能对  
   `connect.rom.miui.com` / `wifi.vivo.com.cn` 的 `generate_204` **各超时约 12s**  
   （门户已在线仍报成功，但手动登录总耗时会被拉到 30s+）。  
   校园环境可改为 **`portal`**，只认认证网关。  
4. 设备上若同时装有 **bitsrunlogin-go** 等其它认证客户端，可能与 smart-srun 抢会话；排查时注意。

---

## 5. 实测时间线摘要（2026-07-20）

| 步骤 | 结果 |
|------|------|
| 未连 NCHU Wi‑Fi 探 `10.1.88.4` | 失败（预期） |
| 连上 `NCHU_Wireless` | STA 获 `10.83.0.0/16` 地址，门户可达 |
| 错误 suffix `1-@…` | 应对失败/配置污染 |
| 改为 `stu.nchu.edu.cn` 后 `srunnet logout` | 成功 |
| 紧接 `srunnet login` | 成功，`rad_user_info` 恢复在线 |
| LuCI / 手动登录全流程 | 成功（含无线重建 + 外网 204 超时警告） |

登出会中断 WAN，远程 SSH 会短暂断开；自动化应在路由本机 **logout 后立即 login** 自愈。

---

## 6. 预设 JSON 意向（与 `doc/school-presets.json` 同步）

```json
{
  "id": "nchu",
  "name": "南昌航空大学（前湖校区）",
  "status": "active",
  "description": "前湖 Wi-Fi 已在 2026-07 实测登录/登出。后缀为 stu.nchu.edu.cn（学生用户），勿使用油猴旧版误抓的 1-@stu.nchu.edu.cn。有线拨号未覆盖。",
  "defaults": {
    "base_url": "http://10.1.88.4",
    "ac_id": "1",
    "ssid": "NCHU_Wireless",
    "access_mode": "wifi"
  },
  "observed_login_shape": {
    "n": "200",
    "type": "1",
    "enc": "srun_bx1",
    "info_prefix": "SRBX1",
    "double_stack": "0"
  },
  "operators": [
    { "suffix": "stu.nchu.edu.cn", "label": "学生用户" }
  ],
  "source_issue": "https://github.com/matthewlu070111/smart-srun/issues/22"
}
```

可选：若后续抓到其它真实 domain，再按「抓一个补一个」追加 `operators`；未验证的电信类套餐继续用手工 `"??"` 而不是猜 `ctcc`。

---

## 7. 网关会话行为实测（2026-07-21，重启自动认证排查）

多次路由器重启 + 抓日志得到的网关（`10.1.88.4`，深澜 theme pro）会话语义：

| 行为 | 实测结论 |
|------|----------|
| 会话归属 | 会话挂在 **AC 的无线关联** 上；路由器**硬重启不发 deauth**，旧会话在 AC 上悬挂数分钟不消失 |
| 重启后 DHCP | 常拿到**不同 IP**（`10.83.0.0/16` 池轮换） |
| 新 IP 登录（旧会话未清） | `E2620: You are already online.`（按账号判定，不是按 IP） |
| 本 IP `rad_user_info`（旧会话未清） | `not_online_error`（按 IP 判定）——与 E2620 并存即"僵局特征" |
| `rad_user_dm` unbind 登出 | **踢不掉**挂在旧 IP 上的会话（多次实测 E2620 依旧） |
| 认证频率限制 | `E2532: The two authentication interval cannot be less than 3 seconds.`（两次认证需间隔 ≥3s，登出后立即重登必中） |
| 唯一有效解法 | **干净的断开重连**（禁用 STA → 重建关联 → 新 DHCP）后登录即成功——AP 上报解除关联，AC 清掉旧会话 |

插件侧对策（1.3.5）：

1. `srun_auth.default_login_once`：E2620 时先查本 IP 在线状态；在线则视为成功（"已在线"）；不在线则 unbind 登出 + **等 3.5s**（避 E2532）再重登。
2. `orchestrator.run_once_with_retry`：重登仍 E2620 时升级为 `stale_session_rebuild` —— 复用手动登录预清理（断开重连 + 新 IP）后再登录，每轮重试循环仅升级一次。
3. 实测重启后 **28~30 秒内全自动恢复认证**（daemon 起动 → 首败 → E2620 → 重建 → 登录成功，两次重启复现）。
4. 网关偶发 `AUTH failed, BAS respond timeout.`（RADIUS 后端抖动）与登录空响应（`no_response_data_error`），与时段/负载相关；对策是重试循环**限次**（默认 `backoff_max_retries=4`、冷却上限 60s）——循环返回主 tick 后下一轮重新进入时可再次触发重建，形成周期性自愈，同时循环间隙状态快照能正常刷新。

另：NCHU `rad_user_info` 返回的在线用户名**带后缀**（`学号@stu.nchu.edu.cn`），在线判定与 LuCI 账号池"已连接"匹配需按 `@` 前主体比对（已在 `_base.parse_online_status` / CBI 处理）。

---

## 8. 明日回校建议

1. 更新油猴脚本 **0.2.1+**，重置该门户 origin 的采集面板后重登，确认导出 suffix 仅为 `stu.nchu.edu.cn`。  
2. 可选：再抓一次「是否存在空后缀校园网」及其它套餐文案。  
3. 有线 / 深澜拨号若需支持，另开议题（与 HTTP portal 路径不同）。  
4. 将 `doc/school-presets.json` 变更同步到：  
   - `root/usr/lib/smart_srun/school_presets_fallback.json`  
   - `cloudflare-pages/public/` 下对应 JSON（若仓库仍维护镜像）
