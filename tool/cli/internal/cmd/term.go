package cmd

import (
	"os"
)

// isTerminal 判断给定文件是否为字符设备（TTY）。用于交互模式守卫。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
