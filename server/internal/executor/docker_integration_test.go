package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dockerAvailable 探测本机是否有可用的 docker CLI + daemon。
// 集成测试在无 docker 环境（如纯开发沙箱）跳过，CI runner（自带 docker）真跑——
// 与 M6-01「CI 装 git 后真跑」同款环境依赖策略（AGENTS.md 豁免）。
func dockerAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "version").Run(); err != nil {
		return false
	}
	return true
}

// integrationDocker 构造一个带较长超时的真实 DockerExecutor（首次跑需拉取镜像）。
func integrationDocker(t *testing.T) *DockerExecutor {
	t.Helper()
	dir := t.TempDir()
	d, err := NewDockerExecutor(dir, DockerOptions{Timeout: 5 * time.Minute})
	if err != nil {
		t.Fatalf("NewDockerExecutor: %v", err)
	}
	return d
}

// TestDockerIntegration_EscapeWriteDenied 验证「逃逸写系统目录」在容器内被拒：
// --read-only 根文件系统下，写 /etc/passwd 必须失败（非零退出 + 只读报错）。
// 这是 M8-02 验收「逃逸命令在容器内被拒」的核心用例之一。
func TestDockerIntegration_EscapeWriteDenied(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("本机无可用 docker，跳过真实容器集成测试（CI/有 docker 环境真跑）")
	}
	d := integrationDocker(t)
	res, err := d.Run(context.Background(), `echo x > /etc/passwd`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("写 /etc/passwd 应被只读根拒绝，却成功了（stdout=%q stderr=%q）", res.Stdout, res.Stderr)
	}
	lower := strings.ToLower(res.Stderr + res.Stdout)
	if !strings.Contains(lower, "read-only") && !strings.Contains(lower, "permission denied") && !strings.Contains(lower, "denied") {
		t.Fatalf("stderr 应含只读/权限拒绝信息：%q", res.Stderr)
	}
}

// TestDockerIntegration_NetworkDenied 验证网络白名单生效：--network none 下
// 容器内出网请求全部失败（busybox wget 报 bad address → || echo NET_DENIED）。
func TestDockerIntegration_NetworkDenied(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("本机无可用 docker，跳过真实容器集成测试")
	}
	d := integrationDocker(t)
	// 10.255.255.1 是保留地址，任何正常网络环境都不可达；--network none 下必然失败。
	res, err := d.Run(context.Background(), `wget -T 3 -O- http://10.255.255.1/ >/dev/null 2>&1 || echo NET_DENIED`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "NET_DENIED") {
		t.Fatalf("--network none 下出网应失败（stdout=%q stderr=%q）", res.Stdout, res.Stderr)
	}
}

// TestDockerIntegration_WorkdirIsolation 验证目录隔离：宿主机 workdir 挂载为
// /workspace 可见；而容器根目录看不到宿主机系统路径（逃逸读取宿主机文件不可行）。
func TestDockerIntegration_WorkdirIsolation(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("本机无可用 docker，跳过真实容器集成测试")
	}
	dir := t.TempDir()
	marker := "gm-agent-marker-8f3a1c"
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDockerExecutor(dir, DockerOptions{Timeout: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	// ① /workspace 能看到挂载内容。
	res, err := d.Run(context.Background(), "cat /workspace/marker.txt")
	if err != nil {
		t.Fatalf("Run(cat marker): %v", err)
	}
	if strings.TrimSpace(res.Stdout) != marker {
		t.Fatalf("容器内读 /workspace/marker.txt = %q, want %q", res.Stdout, marker)
	}

	// ② 容器根目录不含该 marker（挂载点外看不到宿主机文件 → 逃逸读宿主文件不可行）。
	res2, err := d.Run(context.Background(), "ls /")
	if err != nil {
		t.Fatalf("Run(ls /): %v", err)
	}
	if strings.Contains(res2.Stdout, "marker.txt") {
		t.Fatalf("容器根目录不应包含宿主机 marker.txt（stdout=%q）", res2.Stdout)
	}
}

// TestDockerIntegration_RunCommand 验证 argv 直调路径在容器内真实可执行。
func TestDockerIntegration_RunCommand(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("本机无可用 docker，跳过真实容器集成测试")
	}
	d := integrationDocker(t)
	res, err := d.RunCommand(context.Background(), "echo", "hello-argv")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello-argv" {
		t.Fatalf("stdout = %q, want hello-argv", res.Stdout)
	}
}
