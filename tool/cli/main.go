// gmctl 是 GM-Agent 平台的命令行客户端入口。
package main

import (
	"fmt"
	"os"

	"github.com/ayanmw/multiagent2/tool/cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}
