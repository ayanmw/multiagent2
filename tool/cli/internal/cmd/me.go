package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var meCmd = &cobra.Command{
	Use:   "me",
	Short: "查看当前登录用户信息",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		u, err := client.Me(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("ID:           %d\n", u.ID)
		fmt.Printf("用户名:       %s\n", u.Username)
		fmt.Printf("邮箱:         %s\n", u.Email)
		fmt.Printf("显示名:       %s\n", u.DisplayName)
		fmt.Printf("角色:         %s\n", u.Role)
		fmt.Printf("状态:         %s\n", u.Status)
		return nil
	},
}
