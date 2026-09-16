// Command srunnet is the smart-srun 2.0 client.
//
// The CLI and LuCI share the daemon's configuration and authentication workers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/matthewlu070111/smart-srun/core/internal/cli"
)

func main() {
	// SIGTERM is what procd sends to stop the service, and cancelling the
	// context is how that reaches every loop inside it. Nothing here forks or
	// writes a pidfile: a process that backgrounded itself is a process procd
	// could not supervise.
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return cli.RunStatus(ctx, nil, stdout, stderr)
	}

	switch args[0] {
	case "version", "--version", "-V":
		fmt.Fprintln(stdout, cli.VersionString())
		return cli.ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return cli.ExitOK
	case "config":
		if cli.OnlineConfig(args[1:]) {
			return cli.RunOnline(ctx, args, os.Stdin, stdout, stderr)
		}
		return cli.RunConfig(args[1:], stdout, stderr)
	case "login", "logout", "relogin", "switch", "enable", "disable":
		return cli.RunOnline(ctx, args, os.Stdin, stdout, stderr)
	case "daemon":
		return cli.RunDaemon(ctx, args[1:], stdout, stderr)
	case "service":
		return cli.RunService(ctx, args[1:], stdout, stderr)
	case "status":
		return cli.RunStatus(ctx, args[1:], stdout, stderr)
	}

	if cli.IsCoreCommand(args[0]) {
		fmt.Fprintf(stderr,
			"命令 %q 本次构建尚未包含，运行 srunnet help 查看可用命令。\n",
			args[0])
		return cli.ExitUnsupported
	}
	fmt.Fprintf(stderr, "未知命令 %q，运行 srunnet help 查看可用命令。\n", args[0])
	return cli.ExitInvalidInput
}

func usage(out *os.File) {
	fmt.Fprint(out, `SMART SRun (srunnet) —— OpenWrt 深澜校园网认证客户端

配置保存与认证命令共用后台服务；状态和配置读取不会启动服务。

可用命令
  status [--json]          显示状态；不会启动服务
  login|logout|relogin [ID] [--json] [--no-wait] [--ignore-quiet]
                           省略 ID 使用当前校园账号；默认等到动作结束
  switch campus|hotspot [ID] [--json] [--no-wait] [--ignore-quiet]
                           省略 ID 使用对应默认项；成功后保存当前选择
  enable|disable           保存自动认证开关（JSON 结果）
  service ensure-running   启动本项目服务并等待就绪（最多 5 秒）
  service stop             取消进行中的动作并停止本项目服务
  service status           只报告服务是否在运行
  daemon                   在前台运行服务（由 procd 调用）
  config validate [文件]   校验配置；不带文件则读标准输入
  config schema            输出只读字段契约（JSON）
  config defaults          输出默认配置（JSON）
  config show|get [字段路径] 读取已保存配置，隐藏密码（JSON）
  config set               从 JSON 标准输入保存设置
  config account|hotspot list|get ID
                           读取账号或热点，隐藏密码（JSON）
  config account|hotspot add|edit|rm|default
                           从 JSON 标准输入保存账号或热点（JSON 结果）
  version                  显示版本
  help                     显示本帮助

保存输入示例（revision 来自 config show；冲突后请重新读取，不能盲目覆盖）
  config set: {"expected_revision":0,"settings":{"enabled":false}}
  config account add: {"expected_revision":0,"account":{"user_id":"学生账号","password":"密码","wired_iface":"wan"}}
  config account edit: {"expected_revision":1,"account":{"id":"c1","label":"新名称"}}
  config hotspot add: {"expected_revision":2,"profile":{"ssid":"热点","encryption":"none"}}
  rm/default: {"expected_revision":3,"id":"c1"}
密码省略表示保留，空字符串表示清空；密码通过管道或文件输入，请勿放进命令行。

退出码
  0 成功   2 参数或配置无效   3 服务未运行   4 动作失败
  5 冲突或忙   6 能力不支持   130 用户取消
`)
}
