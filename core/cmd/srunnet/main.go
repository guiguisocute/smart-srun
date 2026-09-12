// Command srunnet is the smart-srun 2.0 client.
//
// This build implements the configuration contract only. Commands that need the
// daemon, the protocol or the network are not present yet, and asking for one
// exits 6 (capability unsupported) with a message saying so. A command that
// printed a plausible result without doing the work would be worse than absent.
package main

import (
	"fmt"
	"os"

	"github.com/matthewlu070111/smart-srun/core/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		usage(stdout)
		return cli.ExitOK
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
	}

	if cli.IsCoreCommand(args[0]) {
		fmt.Fprintf(stderr,
			"命令 %q 需要守护进程与网络组件，本次构建尚未包含。\n"+
				"当前可用：srunnet config validate|schema|defaults、srunnet version。\n",
			args[0])
		return cli.ExitUnsupported
	}
	fmt.Fprintf(stderr, "未知命令 %q，运行 srunnet help 查看可用命令。\n", args[0])
	return cli.ExitInvalidInput
}

func usage(out *os.File) {
	fmt.Fprint(out, `SMART SRun (srunnet) —— OpenWrt 深澜校园网认证客户端

本次构建只包含配置契约部分，认证、守护与界面桥接尚未实现。

可用命令
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
