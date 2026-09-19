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

`targets.json` 锁定 SDK 下载哈希、feeds 提交和 Go 工具链。当前录入 x86_64 的 IPK/APK，以及 aarch64_cortex-a53、mips_24kc 的 IPK 构建目标；录入不等于真机验收。`pending_architectures` 仍是待完成的架构，不能宣称已经支持。只有纯 LuCI 包使用 `all`/`noarch`，核心和 bundle 使用实际 SDK 架构。

在 Linux（Python 3.12+、OpenWrt SDK 主机依赖及现有 Go 引导工具链）运行：

```sh
python3 scripts/build_go_sdk.py --target x86_64-opkg-24.10.8 \
  --version 2.0.0rc1 --work-dir /tmp/smart-srun-sdk --bootstrap /usr/local/go
```

工作目录不能有空格。脚本保留构建日志，并输出包、`SHA256SUMS` 和包含源码文件哈希、实际包版本、架构、大小的 `build-record.json`。已记录的同版本产物不会覆盖；构建通过不等于安装或独立验收通过。开发载荷限制为 10 MiB。

MIPS 使用 `GOMIPS=softfloat`，并关闭内联以满足完整载荷上限；其他架构保留正常内联。这会增加部分 CPU 操作耗时，不能将模拟器微基准当作真机延迟或内存验收。比较用例在 `core/tests/performance/`，资源和网络表现仍需在目标设备测量。

`scripts/verify_go_sdk.py` 重新核对原生包元数据、完整载荷、ELF 架构/大小端、静态链接和 Go 构建参数。必须提供构建记录、开发机 Go 和新的报告目录；`--qemu` 或 `--execute` 才运行包内版本命令。APK 另需 `--apk` 和受信公钥目录 `--keys`。输出的 `validation.json` 按包 SHA256 记录 ELF 检查，`inspection.json` 保存具体结果；版本命令成功不等于核心功能、安装或真机通过。

```sh
python3 scripts/verify_go_sdk.py sdk/artifacts/2.0.0rc1/build-record.json \
  --go /usr/local/go/bin/go --qemu /usr/bin/qemu-aarch64 --output checks/arm64
```

APK 目标可通过 `--sign-key` 和 `--public-key` 传入维护者控制的密钥。私钥不得入库或上传到构建产物。脚本对自己的未签名输入执行离线签名，然后必须用公钥通过原生 `apk verify`，不能只相信签名命令的退出码。公钥信任引导、固件安装和发布验收是另外的步骤；不能在安装时加 `--allow-untrusted`。

`.github/workflows/build-go.yml` 是按上述目标生成矩阵的手动构建流程，不发布 Release，默认 APK 未签名。旧的两个发布流程拒绝 Go 源码树，避免生成未经 2.0 验收的公开产物。首个公开 RC 仍需完成剩余架构、更新恢复、资源、真机和独立审查。

### Go 更新与开发部署

`srunnet update check` 异步检查官方发布清单，`update run 计划ID` 启动独立 procd Worker。`update status` 只读固定状态，即使主服务停止也可使用；等待命令被取消不会终止已经开始的原生安装。安装失败时先读取状态和实际包版本，再使用 `update recover` 恢复保留的原版本包；配置备份独立保存，不自动覆盖当前配置。APK 必须通过设备已有受信公钥验证。

发布清单由 `scripts/make_go_manifest.py` 从真实 SDK `build-record.json` 生成。验证报告按包 SHA256 关联，构建通过不会自动变成真机或校园验收通过。未提交源码的构建只能用 `--internal-test` 生成内部测试清单，不得公开发布。

```sh
python3 scripts/make_go_manifest.py sdk-a/artifacts/2.0.0rc1/build-record.json \
  sdk-b/artifacts/2.0.0rc1/build-record.json --evidence validation.json --output release/2.0.0rc1
```

输出目录必须不存在。脚本复制包并重新核对字节，生成清单和 `SHA256SUMS`；上传时使用整个输出目录。发布文件名增加实际包架构和 `_openwrt-<SDK版本>`，避免多架构 APK 原生同名，以及不同 SDK 的同名 IPK 相互覆盖。包内名称、版本和签名不变，构建目录继续保留原生文件名。清单最后写入，未完成的导出不会覆盖已有目录。

构建记录同时保存版本替换前的 `source_template_files` 和实际 SDK 输入的 `source_files`。跨包格式合并时比较原始源码，允许 Makefile 中 opkg `~rc` 与 APK `_rc` 的版本写法不同；其他文件必须一致。缺少原始源码测量的旧记录不能补写猜测值，应重新构建。

成功更新或恢复后，保留最近两次任务的恢复包与独立配置备份，删除已完成任务的临时下载。进行中的任务、损坏的 journal、没有完成记录的旧目录、未知文件与符号链接均不自动清理；清理失败会提示并在下一次更新前重试，不会把已经核验成功的安装说成失败。

Go 树中的 `scripts/hot_update.py` 已转为 SDK 包部署入口，需要设备已安装支持 `update inventory` 的 Go 版本。首次安装使用原生包管理器。开发主机使用 Python 3.11+ 与 OpenSSH，设备无需 Python；SSH 认证、跳板与主机密钥沿用本机配置：

```sh
python3 scripts/hot_update.py --host router \
  --manifest new/release-manifest.json --assets new \
  --recovery-manifest old/release-manifest.json --recovery-assets old \
  --probe
```

`--dry-run` 只校验本地文件；`--probe` 读取设备安装信息并选择包，不上传或安装；`--prepare` 上传并完成设备端核验但不安装。去掉这些参数后执行更新，或加 `--background` 提交后返回。必须同时提供当前精确版本的恢复包，不能混用 bundle 与 split，也不能降级成逐文件覆盖。实际安装仍由独立 Worker 处理，主服务停止不会中断它。
