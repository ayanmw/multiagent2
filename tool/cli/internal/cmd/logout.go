package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "清除本地保存的令牌",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg.Token = ""
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("已退出登录，本地令牌已清除")
		return nil
	},
}
