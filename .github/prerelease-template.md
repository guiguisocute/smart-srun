# smart-srun ${VERSION}

Go 预发布候选，源码 `${SOURCE_COMMIT}`。请维护者补充本次变更、已知问题与验收结论后发布此草稿。

## 安装与更新

先在[下载页](https://srun-doc.guiguisocute.com/guide/download)选择路由器型号，再核对实际固件与包管理器。完整包包含 Go 核心和 LuCI；分体 ZIP 包含同版本核心和 LuCI，两者互斥。核心和完整包需要匹配设备架构，只有纯 LuCI 包与架构无关。

本次提供 ${ASSET_COUNT} 个独立包。架构、SDK、安装载荷和实测层级见 `release-manifest.json`，完整校验和见 `SHA256SUMS`，可复查来源见 `build-records.tar.gz`。编译与版本启动检查不表示所有设备、长期运行或校园认证已通过。

APK 首次安装前，按[安装指南](https://srun-doc.guiguisocute.com/guide/install)核对并安装 `smart-srun-apk.pem`。公钥 SPKI SHA-256：`${APK_FINGERPRINT}`。后续安装和更新保留原生签名校验，不关闭信任验证。

2.x 改用 `/etc/smart-srun/config.json` 与 `/etc/smart-srun/user-presets.json`，不读取或自动迁移 1.x 配置。请备份后重新配置账号。设备运行不依赖 Python；系统中其他插件使用的 Python 包不受影响。

已有 2.x 安装可通过 LuCI 或 `srunnet update` 获取匹配的更新；稳定通道不会自动跳到预发布版。预发布版本仍需结合上述实测范围使用。公开资产不覆盖，问题修复以新的 RC 版本提供。
