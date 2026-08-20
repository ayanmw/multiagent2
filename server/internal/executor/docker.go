package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 执行后端（M8-02）：统一由 NewBackendExecutor 按枚举构造，业务层（CodeAct 工具、
// 子代理）只依赖 Executor 接口，切换后端不改任何调用代码。
const (
	// BackendHost 是默认后端：在宿主机受限工作目录下执行（M1-04 HostExecutor）。
	// cwd 约束 + 危险命令策略 + 超时；无法阻止命令内 cd/绝对路径逃逸（真·文件系统
	// 沙箱需要容器后端，即 BackendDocker）。
	BackendHost Backend = "host"
	// BackendDocker 是容器沙箱后端：命令在一次性容器内执行，容器带
	// `--network none`（无网络，网络白名单最严）+ `--read-only`（只读根文件系统）+
	// 工作目录挂载 `/workspace`（目录隔离）。逃逸命令（写系统目录 / 访问外网 /
	// 读取宿主机文件）在容器内被拒，是对 HostExecutor 的强化替代。
	BackendDocker Backend = "docker"
)

// Backend 是执行后端枚举（M8-02）。合法值：BackendHost / BackendDocker。
type Backend string

// Docker 后端的默认值（config 层可经 env 覆盖；DockerOptions 零值回落以下默认）。
const (
	// DefaultDockerImage 是执行命令的默认容器镜像。
	DefaultDockerImage = "alpine:3.20"
	// DefaultDockerNetwork 是默认网络白名单：none = 容器无网络。
	DefaultDockerNetwork = "none"
	// DefaultDockerBin 是默认 docker CLI 命令名（按 PATH 查找）。
	DefaultDockerBin = "docker"
)

// DockerOptions 是 Docker 执行后端的配置（M8-02）。
// 空值全部回落安全默认：无网络 + 只读根 + alpine 镜像 + docker CLI。
type DockerOptions struct {
	// Image 是执行命令的容器镜像。默认 alpine:3.20（小体积、自带 sh/常用工具）。
	// 注意：容器内没有宿主机工具链；若需 git 等工具，请换成包含它们的镜像
	// （如 DOCKER_IMAGE=alpine/git:latest，或自建 runner 镜像）。
	Image string
	// Network 是 docker run 的 --network 值。默认 "none" = 容器无网络（网络白名单
	// 最严，逃逸类出网请求全部失败）；显式设 "bridge"/"host" 可放行（生产按需）。
	Network string
	// ReadOnly 是否以只读根文件系统运行容器（--read-only）。nil = true（安全默认）；
	// 显式置 false 可关闭（此时仅挂载的 /workspace 之外也可写，一般不建议）。
	ReadOnly *bool
	// Bin 是 docker CLI 可执行文件路径（默认 "docker"，按 PATH 查找）。
	Bin string
	// Timeout 是单次命令执行的超时（默认 60s，与 HostExecutor 一致）。
	Timeout time.Duration
}

// withDefaults 把零值字段回落为安全默认，返回归一化副本（不改原值）。
func (o DockerOptions) withDefaults() DockerOptions {
	if o.Image == "" {
		o.Image = DefaultDockerImage
	}
	if o.Network == "" {
		o.Network = DefaultDockerNetwork
	}
	if o.Bin == "" {
		o.Bin = DefaultDockerBin
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultHostTimeout
	}
	if o.ReadOnly == nil {
		ro := true
		o.ReadOnly = &ro
	}
	return o
}

// readOnly 返回归一化后的只读开关值（nil 已由 withDefaults 填为 true）。
func (o DockerOptions) readOnly() bool {
	return o.ReadOnly == nil || *o.ReadOnly
}

// containerWorkdir 是挂载工作目录在容器内的固定路径。
// 所有命令都在该目录内执行（-w），文件工具以它为根，实现「目录隔离」。
const containerWorkdir = "/workspace"

// ErrDockerUnavailable 是 docker CLI 不可用 / 探测失败时的哨兵错误（M8-02）。
// 调用方可经 errors.Is 判定后给出可读提示（如「当前环境无 docker，请改用
// EXECUTOR_BACKEND=host」），而不是把它当成命令执行失败。
var ErrDockerUnavailable = errors.New("executor: docker 后端不可用")

// dockerRunner 抽象 docker CLI 调用（默认走 exec；测试注入 fake 可离线验证参数拼装）。
// 返回 stdout / stderr / 退出码；启动失败（CLI 缺失等）返回非 nil error 且退出码 -1。
type dockerRunner func(ctx context.Context, args ...string) (stdout, stderr string, exitCode int, err error)

// defaultDockerRunner 用 exec.CommandContext 直接调 docker CLI（argv 形式，不经 shell）。
func defaultDockerRunner(ctx context.Context, args ...string) (string, string, int, error) {
	if len(args) == 0 {
		return "", "", -1, fmt.Errorf("executor: docker 参数为空")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// DockerExecutor 在一次性容器内执行命令，是 Executor 的容器沙箱实现（M8-02）。
//
// 隔离模型（相对 HostExecutor 的核心增强）：
//   - 网络白名单：--network none，容器无网络，出网请求（curl/wget/git clone 外网等）失败；
//   - 文件系统：--read-only，根文件系统只读，写 /etc、/usr、/bin 等系统目录被拒，
//     唯一可写的是挂载的 /workspace（对应宿主机 workdir）；
//   - 目录隔离：命令在容器内执行，cd 到容器根或读取 /etc/passwd 看到的是容器自身
//     内容，而非宿主机——从结构上消除 HostExecutor 无法阻止的 cd/绝对路径逃逸。
//
// 实现走 docker CLI（不引入 docker SDK，保持纯 Go 无 CGO），每次 Run/RunCommand
// 起一个 `docker run --rm` 一次性容器，--cidfile 记录容器 id 供超时清理，避免
// 超时被杀后容器残留。
type DockerExecutor struct {
	workdir string        // 宿主机上的受限工作目录（绝对路径，审计/展示用）
	opts    DockerOptions // 归一化后的容器配置
	runner  dockerRunner  // docker CLI 调用通道（默认 exec；测试注入 fake）
}

// NewDockerExecutor 构造一个容器沙箱执行器。workdir 必须存在且为目录（语义同
// HostExecutor）；opts 空值回落安全默认。构造时不探测 docker 可用性（避免 host
// 装配路径误伤），用 Check() 显式探测，或在首次 Run 时按错误判定。
func NewDockerExecutor(workdir string, opts DockerOptions) (*DockerExecutor, error) {
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("executor: 获取当前工作目录失败: %w", err)
		}
		workdir = wd
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, fmt.Errorf("executor: 解析工作目录失败: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("executor: 工作目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("executor: 工作目录不是目录: %s", abs)
	}
	return &DockerExecutor{
		workdir: abs,
		opts:    opts.withDefaults(),
		runner:  defaultDockerRunner,
	}, nil
}

// Check 探测 docker CLI 与 daemon 是否可用（`docker version` 成功即认为可用）。
// 供 main 在 EXECUTOR_BACKEND=docker 启动时校验；失败返回 ErrDockerUnavailable 包装。
func (d *DockerExecutor) Check() error {
	bin, err := d.lookupBin()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stdout, stderr, exitCode, err := d.runner(ctx, bin, "version", "--format", "{{.Server.Version}}")
	if err != nil || exitCode != 0 {
		return fmt.Errorf("%w: %s %s", ErrDockerUnavailable, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}
	return nil
}

// lookupBin 规范化 docker CLI 路径：显式路径校验存在性；裸命令名（含 / \ 之外的
// 名称）原样返回，由 runner 在启动时按 PATH 解析——这样测试注入 fake runner 时
// 不依赖宿主机真实安装 docker。CLI 缺失的判定放在 runOnce（exec.ErrNotFound）。
func (d *DockerExecutor) lookupBin() (string, error) {
	bin := d.opts.Bin
	if strings.ContainsAny(bin, `/\`) {
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("%w: docker CLI 路径不存在: %s", ErrDockerUnavailable, bin)
		}
	}
	return bin, nil
}

// baseArgs 返回 docker run 的固定隔离参数（网络白名单 / 只读根 / 工作目录挂载）。
func (d *DockerExecutor) baseArgs(cidfile string) []string {
	args := []string{"run", "--rm"}
	if d.opts.Network != "" {
		args = append(args, "--network", d.opts.Network)
	}
	if d.opts.readOnly() {
		args = append(args, "--read-only")
	}
	// --cidfile：把容器 id 写入宿主临时文件，供超时/取消后 docker rm -f 清理，防残留。
	if cidfile != "" {
		args = append(args, "--cidfile", cidfile)
	}
	// 挂载宿主机 workdir → 容器 /workspace；-w 使命令默认在 /workspace 内执行。
	args = append(args, "-v", dockerVolumePath(d.workdir)+":"+containerWorkdir, "-w", containerWorkdir)
	return args
}

// dockerVolumePath 把宿主机绝对路径转成 docker CLI 可接受的挂载源路径：
// Windows 盘符路径 C:\Users\x 转 C:/Users/x（docker 客户端不认反斜杠盘符）；
// 类 Unix 原样返回。
func dockerVolumePath(p string) string {
	if filepath.Separator == '\\' {
		return filepath.ToSlash(p)
	}
	return p
}

// runOnce 执行一次 docker 调用并统一映射结果：正常/非零退出为有效结果；
// 超时则 ExitCode=-1 并清理残留容器；docker 基础设施故障（daemon/镜像/权限）
// 返回 ErrDockerUnavailable；启动失败返回原始错误。containerArgs 以
// "run", ... 开头（不含 bin），由调用方组装。
func (d *DockerExecutor) runOnce(ctx context.Context, bin string, containerArgs []string, cidfile string) (*Result, error) {
	timeout := d.opts.Timeout
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{bin}, containerArgs...)
	stdout, stderr, exitCode, err := d.runner(runCtx, args...)
	res := &Result{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			d.cleanupContainer(cidfile, bin)
			res.ExitCode = -1
			return res, fmt.Errorf("executor: 命令执行超时(>%s): %w", timeout, context.DeadlineExceeded)
		}
		// docker CLI 缺失/不可执行（exec.ErrNotFound）→ 明确报「后端不可用」，
		// 而不是当成命令执行失败，便于上层给出「改用 EXECUTOR_BACKEND=host」的提示。
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: docker CLI %q 未找到或不可执行: %v", ErrDockerUnavailable, bin, err)
		}
		// 启动失败（权限等）。
		return nil, fmt.Errorf("executor: docker 命令启动失败: %w", err)
	}
	if exitCode != 0 && isDockerInfraError(exitCode, stderr) {
		return nil, fmt.Errorf("%w: %s", ErrDockerUnavailable, strings.TrimSpace(stderr))
	}
	return res, nil
}

// Run 在一次性容器内执行命令（容器内 shell 固定为 sh -c，与宿主机平台无关——
// 容器是 Linux）。返回 Result：非零退出码为有效结果；超时则 ExitCode=-1 并清理
// 残留容器；docker CLI 不可用（找不到可执行文件）返回 ErrDockerUnavailable。
func (d *DockerExecutor) Run(ctx context.Context, command string) (*Result, error) {
	if command == "" {
		return nil, fmt.Errorf("executor: 命令不能为空")
	}
	bin, err := d.lookupBin()
	if err != nil {
		return nil, err
	}
	cidfile, err := newCIDFile()
	if err != nil {
		return nil, err
	}
	defer os.Remove(cidfile)

	args := append(d.baseArgs(cidfile), d.opts.Image, "sh", "-c", command)
	return d.runOnce(ctx, bin, args, cidfile)
}

// RunCommand 以 argv 形式在容器内直接执行 name + args（不经 shell 分词），
// 其余语义与 Run 一致。适用于 git 等需精确传递含空格参数的外部程序。
func (d *DockerExecutor) RunCommand(ctx context.Context, name string, args ...string) (*Result, error) {
	if name == "" {
		return nil, fmt.Errorf("executor: 程序名不能为空")
	}
	bin, err := d.lookupBin()
	if err != nil {
		return nil, err
	}
	cidfile, err := newCIDFile()
	if err != nil {
		return nil, err
	}
	defer os.Remove(cidfile)

	runArgs := append(d.baseArgs(cidfile), d.opts.Image, name)
	runArgs = append(runArgs, args...)
	return d.runOnce(ctx, bin, runArgs, cidfile)
}

// Workdir 返回受限工作目录在宿主机上的绝对路径（审计/展示用）。
func (d *DockerExecutor) Workdir() string { return d.workdir }

// newCIDFile 创建一个临时 cidfile，返回其路径（调用方负责删除）。
func newCIDFile() (string, error) {
	f, err := os.CreateTemp("", "gm-agent-docker-cid-*")
	if err != nil {
		return "", fmt.Errorf("executor: 创建容器 id 临时文件失败: %w", err)
	}
	name := f.Name()
	f.Close()
	return name, nil
}

// cleanupContainer 在超时/取消后按 cidfile 记录的容器 id 强制删除容器（best-effort，
// 失败仅记日志，不阻断返回超时错误）。docker run 进程被 kill 时 --rm 不会触发清理，
// 必须显式 docker rm -f，否则容器残留。
func (d *DockerExecutor) cleanupContainer(cidfile, bin string) {
	id, err := os.ReadFile(cidfile)
	if err != nil || len(bytes.TrimSpace(id)) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, stderr, exitCode, err := d.runner(ctx, bin, "rm", "-f", strings.TrimSpace(string(id)))
	if err != nil || exitCode != 0 {
		log.Printf("[executor] docker 超时清理容器失败 id=%s: %s", strings.TrimSpace(string(id)), strings.TrimSpace(stderr))
	}
}

// isDockerInfraError 从「非零退出码 + stderr」判定这是 docker 基础设施故障
// （daemon 未启动 / 镜像不存在 / 权限不足），而非容器内命令的真实非零退出。
// docker run 的故障退出码约定：125=daemon 错误、126=容器内命令无法执行、127=命令不存在。
// 为避免误判，除退出码外还要求 stderr 命中 docker 报错特征短语。
func isDockerInfraError(exitCode int, stderr string) bool {
	if exitCode != 125 && exitCode != 126 && exitCode != 127 {
		return false
	}
	lower := strings.ToLower(stderr)
	for _, phrase := range []string{
		"cannot connect to the docker daemon",
		"unable to connect",
		"docker daemon is not running",
		"no such image",
		"permission denied",
		"error response from daemon",
		"unable to find image",
		"is not a valid repository/tag",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// NewBackendExecutor 按后端枚举构造执行器（M8-02 配置切换入口）。
// backend 非法/空值回落 BackendHost（向后兼容：旧部署不设 EXECUTOR_BACKEND 行为不变）。
func NewBackendExecutor(backend Backend, workdir string, dopts DockerOptions) (Executor, error) {
	if backend != BackendDocker {
		return NewHostExecutor(workdir)
	}
	return NewDockerExecutor(workdir, dopts)
}
