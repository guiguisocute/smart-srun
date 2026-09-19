# 贡献指南

详细说明统一维护在 [智慧深澜文档站](https://srun-doc.guiguisocute.com/contribute/)。

- 功能修复与学校预设数据提交到本仓库；文档修改提交到 [smartsrun-doc](https://github.com/guiguisocute/smartsrun-doc)。
- [贡献学校预设](https://srun-doc.guiguisocute.com/contribute/presets)：使用插件一键配置的 Issue 草稿，记录实际参数与验证范围。
- [架构与认证策略扩展](https://srun-doc.guiguisocute.com/development/architecture)、[测试与发布](https://srun-doc.guiguisocute.com/development/validation)。
- 提交前核对本人 Git 作者身份；合并外部贡献保留原作者署名，不改写已发布历史。PR 说明具体问题、最终行为与验证结果。

## `go` 分支（smart-srun 2.0）

`go` 是 2.0 的开发分支，认证核心改用 Go，LuCI 界面保持不变。

- `root/usr/lib/smart_srun/**` 与 `tests/**` 仍是 1.6.0 的 Python 基线，本指南原有的 Python 约束继续适用。它是行为基线，不要顺手重构。
- `core/**` 是 Go 实现，不适用 Python 专用约束（裸名导入、`python3-light` 标准库限制、UCI 字符串配置等）。
- `root/usr/lib/lua/**` 与 `root/www/**` 两边共用：布局、字段、操作流程、中文文案与五步向导**冻结**，只改后端调用；前端仍是无依赖 ES5。
- 配置 v2 是有意的破坏性更新（`/etc/smart-srun/config.json`，强类型 JSON），不读 1.x 配置。
- 发行包内不得包含或调用 Python；Python 只作开发机工具。
- Go 门禁：`scripts/verify-go.sh`（gofmt + vet + 打乱顺序的单元测试 + 覆盖率 + race）。缺少工具链或 `core/` 时该脚本失败而不是跳过。
- 开发节奏按当前维护者要求及 `.codex/go-loop/next.md` / D22：按完整用户流程分批实现，开发时保留编译、相关已有测试和危险副作用的针对性检查；逐卡补覆盖率、变异测试和独立验收后移至批次加固/发布验收，不再阻挡后续实现。延期检查要明确记录，未经独立审查不得标 passed；界面冻结、数据/网络及最终发布契约仍适用。
- 面向 2.0 的 PR 请说明对应的任务卡编号、执行过的门禁命令与退出码。

### Go SDK 开发包

`targets.json` 锁定 SDK 下载哈希、feeds 提交和 Go 工具链。当前录入的 x86_64 目标用于验证 IPK/APK 安装链，`pending_architectures` 仍是待完成的架构，不能宣称已经支持。只有纯 LuCI 包使用 `all`/`noarch`，核心和 bundle 使用实际 SDK 架构。

在 Linux（Python 3.12+、OpenWrt SDK 主机依赖及现有 Go 引导工具链）运行：

```sh
python3 scripts/build_go_sdk.py --target x86_64-opkg-24.10.8 \
  --version 2.0.0rc1 --work-dir /tmp/smart-srun-sdk --bootstrap /usr/local/go
```

工作目录不能有空格。脚本保留构建日志，并输出包、`SHA256SUMS` 和包含源码文件哈希、实际包版本、架构、大小的 `build-record.json`。已记录的同版本产物不会覆盖；构建通过不等于安装或独立验收通过。开发载荷限制为 10 MiB。

APK 目标可通过 `--sign-key` 和 `--public-key` 传入维护者控制的密钥。私钥不得入库或上传到构建产物。脚本对自己的未签名输入执行离线签名，然后必须用公钥通过原生 `apk verify`，不能只相信签名命令的退出码。公钥信任引导、固件安装和发布验收是另外的步骤；不能在安装时加 `--allow-untrusted`。

`.github/workflows/build-go.yml` 是按上述目标生成矩阵的手动构建流程，不发布 Release，默认 APK 未签名。旧的两个发布流程拒绝 Go 源码树，避免生成未经 2.0 验收的公开产物。首个公开 RC 仍需完成剩余架构、更新恢复、资源、真机和独立审查。
