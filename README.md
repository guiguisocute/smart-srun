# luci-app-smart-srun

智慧深澜，OpenWrt 深澜校园网（SRun 4000）自动认证客户端，提供 CLI / LuCI 两种使用方式。

感谢 [@guiguisocute](https://github.com/guiguisocute) 的协助！

## 预览

### LuCI Web 界面
<p align="center">
    <img src="https://r2.guiguisocute.cloud/PicGo/2026/03/25/aaa7b014a8a2db7fcc05edb9f81c242b.png">
</p>

## 功能

- 自动校园网认证，自动检测断线并重连，支持有线 / 无线接入
- 多校园网账号、多热点配置管理，一键登录登出切换
- 可配置夜间时段自动切换到热点，恢复后自动切回校园网（适应宿舍定时断网环境）
- 结构化运行日志落盘（`/var/log/smart_srun.log`）
- 完整 CLI支持（`srunnet`）：状态查询、登录登出、配置管理、账号 / 热点 CRUD，功能对齐 LuCI
- 支持多学校配置文件，可扩展适配其他深澜校园网环境

### 未来功能
- 加入TLS支持以应对HTTPS认证页面的环境
- 适配 UA3F应对多设备检查的环境
- 支持多号多拨负载均衡网络叠加
- 适配更多高校的深澜校园网环境
- 更多账号功能，如账号分组、规则管理
- ……

## 安装包说明

仓库构建产出三个 ipk 包：

| 包名 | 说明 | 依赖 |
|------|------|------|
| `smart-srun` | 基础包：守护进程 + CLI | `python3-light` |
| `luci-app-smart-srun` | 标准 LuCI Web 界面包（用于 opkg / LuCI 软件包管理升级） | `smart-srun`、LuCI 运行环境 |
| `luci-app-smart-srun-bundle` | 自包含安装包：CLI + LuCI 一起打包，适合手动下载安装 | `python3-light`、LuCI 运行环境 |

- 只需要 CLI：安装 `smart-srun` 即可
- 通过 LuCI / opkg 正常升级：安装 `smart-srun` + `luci-app-smart-srun`
- 手动下载安装、想少折腾：直接安装 `luci-app-smart-srun-bundle`

## 安装与使用

### 安装
**以下操作请在路由器连上互联网的情况进行！**

1. 下载最新 ipk 包：[Releases](https://github.com/matthewlu070111/luci-app-smart-srun/releases)
2. 安装：
   #### 使用 LuCI 网页面板安装：
   1. 登录 LuCI 界面，进入 **系统**——**软件包** 页面。
   2. 点击 **更新列表** 按钮，等待`opkg update`完成。
   3. 点击 **上传软件包...** 按钮，选择自己下载的 ipk 包。
    - 如果需要LuCI Web界面，请**先**安装 `smart-srun`的ipk， **再**安装 `luci-app-smart-srun` 的ipk。
    - 如果你想直接安装一个包完成部署，直接上传 `luci-app-smart-srun-bundle` 的ipk。（推荐）
   4. 点击 **安装** 按钮，等待安装完成。
   5. 出现安装成功的弹窗后，退出 LuCI 界面，重新登录，使新软件包生效。

   #### 使用命令行界面安装：
   6. 将ipk文件上传到OpenWrt设备，并切换到设备目录，执行：
   ```sh
    # 仅 CLI
    opkg install smart-srun_*.ipk
    ```
    ```sh
    # 标准 split 安装
    opkg install smart-srun_*.ipk
    opkg install luci-app-smart-srun_*.ipk
    ```
    ```sh
    # bundle版本单ipk文件安装
    opkg install luci-app-smart-srun-bundle_*.ipk
    ```
   7. 启用服务：
   ```sh
    /etc/init.d/smart_srun enable
    /etc/init.d/smart_srun restart
    ```

安装建议：
- **不要把 `luci-app-smart-srun-bundle` 和标准 split 包混装**

### LuCI 使用

在 LuCI 页面进入 **服务 → SMART SRun**，在「基础设置」标签页中：

- **登录配置**：选择学校
- **校园网账号**：添加学工号、密码、运营商，支持多账号管理
- **热点配置**：配置个人热点 SSID 和密码，供夜间自动切换使用
- **手动登录 / 登出**：随时触发，带进度反馈弹窗
- **手动切网**：一键切到热点或切回校园网

保存并应用后守护进程自动启动。

### CLI 使用

安装后可直接使用 `srunnet` 命令（无参数等同 `srunnet status`）：

```sh
# 查看当前状态
srunnet
srunnet status

# 查看当前安装包类型与版本
srunnet --version

# 登录 / 登出 / 重新登录
srunnet login
srunnet logout
srunnet relogin

# 查看实时日志（Ctrl+C 退出）
srunnet log

# 查看最近 50 行日志
srunnet log -n 50

# 启用 / 禁用守护服务
srunnet enable
srunnet disable

# 手动切换网络
srunnet switch hotspot
srunnet switch campus

# 列出可用学校配置（JSON 输出）
srunnet schools
```

#### 配置管理

```sh
# 查看完整配置
srunnet config
srunnet config show

# 查询 / 设置单个标量值
srunnet config get interval
srunnet config set interval=30 enabled=1

# 从 JSON 文件导入配置
srunnet config set -f my_config.json

# 校园网账号管理
srunnet config account              # 列出所有账号
srunnet config account add          # 交互式添加账号
srunnet config account edit campus-1
srunnet config account rm campus-2
srunnet config account default campus-1

# 热点配置管理
srunnet config hotspot              # 列出所有热点
srunnet config hotspot add          # 交互式添加热点
srunnet config hotspot edit hotspot-1
srunnet config hotspot rm hotspot-2
srunnet config hotspot default hotspot-1
```

## License
WTFPL

****
## 参与贡献
如果你愿意参与开发：请参阅 [贡献指南](/doc/CONTRIBUTING.md)
