# smart-srun ${VERSION}

Go 2.0 预发布候选，源码 `${SOURCE_COMMIT}`。本 Release 保持草稿，供维护者检查安装包、已知问题及验收范围。

## 资源优化基准

同一 OpenWrt 25.12.2 x86_64 基础镜像的配对测量中，完整安装增量（含新增依赖）从 **22,724 KiB 降至 10,240 KiB，减少 54.94%**；单账号守护进程 RSS p95 从 **20,784 KiB 降至 14,040 KiB，减少 32.45%**。

| 稳定维护场景 | Python 1.6 基线 | Go 候选版 |
| --- | ---: | ---: |
| 单账号守护进程 CPU | 0.2166% | 0.1533% |
| 单账号 CPU，含已等待子进程 | 0.3400% | 0.1767% |
| 四账号守护进程 RSS p95 | 20,852 KiB | 15,652 KiB |
| 四账号 CPU，含已等待子进程 | 0.7017% | 0.4250% |

上述测量来自重写期间的 Python `9da28b5` 与 Go `1252165`（内部 rc31），**不是当前 rc1 的重新测量**。运行环境为 128 MiB / 1 CPU TCG 虚拟机；每场景预热 5 分钟、观察 10 分钟、门户检测间隔 60 秒。安装增量是文件系统分配量；RSS 只计守护进程，不是整个插件的峰值。完整方法、来源与边界见[资源基准](https://srun-doc.guiguisocute.com/development/benchmarks)。

## 本次功能

- 设备运行时改为 Go，移除本项目的 Python 运行依赖；认证、配置、后台任务与更新使用统一服务。
- LuCI 进阶设置提供配置导出、预览和导入；可显式导入 1.6.1 备份，保留密码、运营商后缀、账号关联与认证参数。导入后自动守护关闭，避免换设备后立即认证或切网。
- 学校预设支持每日自动更新时间与手动刷新；页面读取不触发远端拉取。
- 无线详情显示实际关联信息；下载页按路由器型号、固件和包管理器匹配原生包。

真实校园、无线漫游、全硬件长期运行和独立审查的结论以实际证据为准。切换出口导致 IP/NAT 改变时，游戏连接仍可能中断，不承诺无缝续传。

## 安装与更新

先在[下载页](https://srun-doc.guiguisocute.com/guide/download)选择路由器型号，再核对实际固件与包管理器。完整包包含 Go 核心和 LuCI；分体 ZIP 包含同版本核心和 LuCI，两者互斥。核心和完整包需要匹配设备架构，只有纯 LuCI 包与架构无关。

本次提供 ${ASSET_COUNT} 个独立包。架构、SDK、安装载荷和实测层级见 `release-manifest.json`，完整校验和见 `SHA256SUMS`，可复查来源见 `build-records.tar.gz`。编译与版本启动检查不表示所有设备、长期运行或校园认证已通过。

APK 首次安装前，按[安装指南](https://srun-doc.guiguisocute.com/guide/install)核对并安装 `smart-srun-apk.pem`。公钥 SPKI SHA-256：`${APK_FINGERPRINT}`。后续安装和更新保留原生签名校验，不关闭信任验证。

2.x 改用 `/etc/smart-srun/config.json` 与 `/etc/smart-srun/user-presets.json`，不自动读取或迁移旧配置。请先在 1.6.1 导出备份，再按[备份与升级指南](https://srun-doc.guiguisocute.com/guide/backup-migration)导入或重新配置账号。设备运行不依赖 Python；系统中其他插件使用的 Python 包不受影响。

已有 2.x 安装可通过 LuCI 或 `srunnet update` 获取匹配的更新；稳定通道不会自动跳到预发布版。预发布版本仍需结合上述实测范围使用。公开资产不覆盖，问题修复以新的 RC 版本提供。
