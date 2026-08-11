package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	loginAccount  string
	loginPassword string
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "登录并保存令牌到本地配置",
	RunE: func(cmd *cobra.Command, args []string) error {
		if loginAccount == "" {
			return fmt.Errorf("请通过 --account 指定账号（用户名或邮箱）")
		}
		if loginPassword == "" {
			return fmt.Errorf("请通过 --password 指定密码（脚本场景建议用环境变量或管道传入）")
		}
		ctx := context.Background()
		r, err := client.Login(ctx, loginAccount, loginPassword)
		if err != nil {
			return err
		}
		cfg.Token = r.Token
		cfg.Account = r.User.Username
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("登录成功：用户 %s（角色 %s）\n", r.User.Username, r.User.Role)
		fmt.Printf("令牌已保存到 %s\n", cfg.PathSafe())
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginAccount, "account", "", "账号（用户名或邮箱），必填")
	loginCmd.Flags().StringVar(&loginPassword, "password", "", "密码，必填")
}
