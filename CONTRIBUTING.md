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
- 面向 2.0 的 PR 请说明对应的任务卡编号、执行过的门禁命令与退出码。
