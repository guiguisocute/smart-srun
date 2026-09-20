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

1. 从 [Releases](https://github.com/matthewlu070111/smart-srun/releases) 下载 `luci-app-smart-srun-bundle`：opkg 选择 `.ipk`，apk 选择 `.apk`。
2. 在 LuCI **系统 → 软件包** 更新列表并上传安装，完成后重新登录。依赖或签名问题见 [安装指南](https://srun-doc.guiguisocute.com/guide/install)。
3. 打开 **服务 → SMART SRun → 开始一键配置**，选择接入方式，确认认证地址与后缀，填写账号并保存。
4. 检查默认账号和 **启用** 状态，点击 **立即登录**。配置成功后，可通过向导提交学校预设。

账号、无线、多 WAN 配置与开发说明请访问 [智慧深澜文档站](https://srun-doc.guiguisocute.com/)；参与项目见 [贡献说明与致谢](https://srun-doc.guiguisocute.com/contribute/)。

![智慧深澜 LuCI 界面，包含校园网账号与热点配置](doc/img/smart-srun-overview.png)

### 1.6.1 配置备份

进阶设置中的「配置备份」可导出含账号与热点密码的 JSON，选择文件后先预览，再确认导入。导入会替换配置并关闭自动守护；先检查账号与网口，再启用。备份包含明文凭据，请勿公开。自建学校预设和系统网络配置需单独备份。

CLI：`srunnet config export /tmp/backup.json`，`srunnet config import /tmp/backup.json --check`；正式导入可带 `--expected-revision` 使用预览版本，避免覆盖其他页面的新修改。Go 2.0 支持显式导入该格式；反向导入不支持。
