// Command srunnet is the smart-srun 2.0 client.
//
// This build has the configuration contract, the local control protocol, the
// scheduler and the service lifecycle. It does not yet have the authentication
// worker behind them: an action submitted here queues, runs and fails saying
// so. A command that printed a plausible result without doing the work would be
// worse than one that is absent.
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
		return cli.RunConfig(args[1:], stdout, stderr)
	case "daemon":
		return cli.RunDaemon(ctx, args[1:], stdout, stderr)
	case "service":
		return cli.RunService(ctx, args[1:], stdout, stderr)
	case "status":
		return cli.RunStatus(ctx, args[1:], stdout, stderr)
	}

	if cli.IsCoreCommand(args[0]) {
		fmt.Fprintf(stderr,
			"命令 %q 需要认证执行组件，本次构建尚未包含。\n"+
				"当前可用：srunnet status、service、daemon、config、version。\n",
			args[0])
		return cli.ExitUnsupported
	}
	fmt.Fprintf(stderr, "未知命令 %q，运行 srunnet help 查看可用命令。\n", args[0])
	return cli.ExitInvalidInput
}

func usage(out *os.File) {
	fmt.Fprint(out, `SMART SRun (srunnet) —— OpenWrt 深澜校园网认证客户端

本次构建包含配置、控制协议、调度与服务生命周期；认证执行与界面桥接尚未实现。

可用命令
  status [--json]          显示状态；不会启动服务
  service ensure-running   启动本项目服务并等待就绪（最多 5 秒）
  service stop             取消进行中的动作并停止本项目服务
  service status           只报告服务是否在运行
  daemon                   在前台运行服务（由 procd 调用）
  config validate [文件]   校验配置；不带文件则读标准输入
  config schema            输出只读字段契约（JSON）
  config defaults          输出默认配置（JSON）
  version                  显示版本
  help                     显示本帮助

退出码
  0 成功   2 参数或配置无效   3 服务未运行   4 动作失败
  5 冲突或忙   6 能力不支持   130 用户取消
`)
}
