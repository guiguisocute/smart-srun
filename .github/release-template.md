# smart-srun ${VERSION}

Go 正式版候选，源码 `${SOURCE_COMMIT}`。请维护者补充本次变更与验收结论后发布此草稿。

## 安装与更新

先在[下载页](https://srun-doc.guiguisocute.com/guide/download)选择路由器型号，并核对实际固件与包管理器。完整包包含 Go 核心和 LuCI；分体 ZIP 包含同版本核心和 LuCI，两种安装方式互斥。核心和完整包必须匹配设备架构，只有纯 LuCI 包与架构无关。

本次提供 ${ASSET_COUNT} 个独立包，具体架构、SDK、校验和与实测层级见 `release-manifest.json`。SDK 编译和版本启动检查不代表全部设备或校园环境已经验收。`SHA256SUMS` 同时覆盖安装包、分体 ZIP、公钥、清单和构建记录。

APK 首次安装前，请按[安装指南](https://srun-doc.guiguisocute.com/guide/install)核对并安装 `smart-srun-apk.pem` 公钥。公钥 SPKI SHA-256：`${APK_FINGERPRINT}`。安装和后续更新始终使用包管理器原生信任校验。IPK 文件校验和提供完整性核对，不等于软件源签名。

2.x 使用 `/etc/smart-srun/config.json` 和 `/etc/smart-srun/user-presets.json`，不读取或自动迁移 1.x 配置。首次升级请先备份，再重新配置账号。设备运行不依赖 Python，也不会删除其他插件所需的系统 Python 包。

2.x 用户可通过 LuCI 或 `srunnet update` 获取同包型、架构及固件兼容的更新。资产一经公开不覆盖；修复使用新版本。
