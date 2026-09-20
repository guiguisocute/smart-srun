<p align="center">
  <a href="https://srun-doc.guiguisocute.com/"><img src="doc/img/logo.svg" width="96" height="96" alt="智慧深澜 Logo"></a>
</p>

<h1 align="center">智慧深澜 · smart-srun</h1>

<p align="center">OpenWrt 深澜校园网认证插件，支持有线、无线与多账号管理。</p>

<p align="center">
  <a href="https://srun-doc.guiguisocute.com/"><strong>访问文档站</strong></a> ·
  <a href="https://github.com/matthewlu070111/smart-srun/releases">下载安装</a> ·
  <a href="https://srun-doc.guiguisocute.com/guide/troubleshooting">故障排查</a>
</p>

## 使用

`go` 分支正在开发 smart-srun 2.0：Go 认证服务与 CLI，保留 LuCI 操作界面，设备无需 Python。开发分支和构建产物不代表已经通过完整发布验收。

1. 在[下载页](https://srun-doc.guiguisocute.com/guide/download)选择路由器型号，核对实际固件、包架构和包管理器：opkg 选择 `.ipk`，apk 选择 `.apk`。只安装与设备匹配的 `luci-app-smart-srun-bundle`；不能将含 Go 核心的包当作 `all` 通用包。
2. 在 LuCI **系统 → 软件包** 更新列表并上传安装，完成后重新登录。依赖或签名问题见 [安装指南](https://srun-doc.guiguisocute.com/guide/install)。
3. 打开 **服务 → SMART SRun → 开始一键配置**，选择接入方式，确认认证地址与后缀，填写账号并保存。
4. 检查默认账号和 **启用** 状态，点击 **立即登录**。配置成功后，可通过向导提交学校预设。

**进阶设置 → 自动更新学校预设** 可选择每天的更新时间（北京时间、24 小时制，默认 09:00）。打开页面只读取本地预设；关闭自动更新后仍可点击 **立即更新**。错过时间后补查一次，失败当天不自动重试；服务重启不会重复检查，设备重启会清空临时缓存和当天记录。

2.0 使用 `/etc/smart-srun/config.json`，不自动读取或迁移 1.x 配置。首次升级可在 1.6.1 的进阶设置导出备份，再在 2.0 中预览并导入；导入后核对账号和网口，再启用自动守护。APK 可手动跳过签名验证安装，公钥为可选项；使用原生验签或内置更新器前需安装项目公钥，详见[安装与校验](https://srun-doc.guiguisocute.com/guide/download#安装与校验)。

账号、无线、多 WAN 配置与开发说明请访问 [智慧深澜文档站](https://srun-doc.guiguisocute.com/)；参与项目见 [贡献说明与致谢](https://srun-doc.guiguisocute.com/contribute/)。

![智慧深澜 LuCI 界面，包含校园网账号与热点配置](doc/img/smart-srun-overview.png)
