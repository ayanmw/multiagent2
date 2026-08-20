package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDockerRecorder 记录 docker CLI 收到的全部 argv，并模拟返回。
type fakeDockerRecorder struct {
	calls   [][]string // 每次调用的完整 argv（含 bin）
	version string     // docker version 返回的 stdout
	// runHook 在收到 "run" 子命令时调用，返回 (stdout, stderr, exitCode, err)；
	// 为 nil 时默认返回 ("ok", "", 0, nil)。
	runHook func(args []string) (string, string, int, error)
	// err 非 nil 时每次调用直接返回该错误（模拟 CLI 缺失/启动失败）。
	err error
}

func (f *fakeDockerRecorder) runner() dockerRunner {
	return func(_ context.Context, args ...string) (string, string, int, error) {
		f.calls = append(f.calls, args)
		if f.err != nil {
			return "", "", -1, f.err
		}
		if len(args) >= 2 && args[1] == "version" {
			return f.version, "", 0, nil
		}
		if len(args) >= 2 && args[1] == "run" {
			if f.runHook != nil {
				return f.runHook(args)
			}
			return "ok", "", 0, nil
		}
		return "", "unknown command", 125, nil
	}
}

// lastRun 返回最后一次 docker run 调用的 argv（不含 bin）。
func (f *fakeDockerRecorder) lastRun() []string {
	for i := len(f.calls) - 1; i >= 0; i-- {
		if len(f.calls[i]) >= 2 && f.calls[i][1] == "run" {
			return f.calls[i][2:]
		}
	}
	return nil
}

// newTestDocker 构造一个注入 fake runner 的 DockerExecutor。
func newTestDocker(t *testing.T, opts DockerOptions, rec *fakeDockerRecorder) *DockerExecutor {
	t.Helper()
	dir := t.TempDir()
	d, err := NewDockerExecutor(dir, opts)
	if err != nil {
		t.Fatalf("NewDockerExecutor: %v", err)
	}
	d.runner = rec.runner()
	return d
}

func TestDockerExecutor_Run_IsolationArgs(t *testing.T) {
	rec := &fakeDockerRecorder{}
	d := newTestDocker(t, DockerOptions{}, rec)

	res, err := d.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil || res.ExitCode != 0 {
		t.Fatalf("res = %+v, want exit 0", res)
	}
	run := rec.lastRun()
	if run == nil {
		t.Fatal("docker run 未被调用")
	}
	joined := strings.Join(run, " ")
	for _, want := range []string{
		"--rm",                        // 一次性容器
		"--network none",              // 网络白名单最严：无网络
		"--read-only",                 // 只读根文件系统
		"--cidfile",                   // 容器 id 记录（超时清理）
		"-v " + filepath.ToSlash(d.workdir) + ":/workspace", // 工作目录挂载
		"-w /workspace",               // 容器内工作目录
		"alpine:3.20",                 // 默认镜像
		"sh -c echo hello",            // 容器内 shell 执行
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker run 参数缺少 %q：%s", want, joined)
		}
	}
}

func TestDockerExecutor_Run_StdoutStderrExitCode(t *testing.T) {
	rec := &fakeDockerRecorder{
		runHook: func(args []string) (string, string, int, error) {
			return "out1", "err1", 7, nil
		},
	}
	d := newTestDocker(t, DockerOptions{}, rec)
	res, err := d.Run(context.Background(), "false")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "out1" || res.Stderr != "err1" || res.ExitCode != 7 {
		t.Fatalf("res = %+v, want stdout=out1 stderr=err1 exit=7", res)
	}
}

func TestDockerExecutor_RunCommand_ArgvDirect(t *testing.T) {
	rec := &fakeDockerRecorder{}
	d := newTestDocker(t, DockerOptions{}, rec)

	// 含空格/中文的提交说明必须作为单一 argv 传递，不得被 shell 重新分词。
	res, err := d.RunCommand(context.Background(), "git", "commit", "-m", "add hello world 中文")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if res == nil {
		t.Fatal("res 为空")
	}
	run := rec.lastRun()
	if run == nil {
		t.Fatal("docker run 未被调用")
	}
	// argv 中 image 之后应为 git commit -m "add hello world 中文"（image 之前的
	// 隔离参数不影响；关键是与 shell 字符串传递不同，整条命令不会被重新分词）。
	imgIdx := -1
	for i, a := range run {
		if a == "alpine:3.20" {
			imgIdx = i
			break
		}
	}
	if imgIdx < 0 {
		t.Fatalf("argv 中未找到镜像 alpine:3.20：%v", run)
	}
	cmd := run[imgIdx+1:]
	want := []string{"git", "commit", "-m", "add hello world 中文"}
	if len(cmd) != len(want) {
		t.Fatalf("image 后 argv = %v, want %v", cmd, want)
	}
	for i := range want {
		if cmd[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q（完整 argv: %v）", i, cmd[i], want[i], run)
		}
	}
}

func TestDockerExecutor_Run_DockerMissing(t *testing.T) {
	// runner 模拟 CLI 缺失（exec.ErrNotFound）→ Run 报 ErrDockerUnavailable。
	rec := &fakeDockerRecorder{err: exec.ErrNotFound}
	d := newTestDocker(t, DockerOptions{}, rec)
	_, err := d.Run(context.Background(), "echo hi")
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v, want ErrDockerUnavailable", err)
	}

	// Bin 指向不存在的显式路径 → lookupBin 阶段即报不可用。
	d2 := newTestDocker(t, DockerOptions{Bin: filepath.Join(t.TempDir(), "no-such-docker")}, &fakeDockerRecorder{})
	if _, err := d2.Run(context.Background(), "echo hi"); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v, want ErrDockerUnavailable", err)
	}
}

func TestDockerExecutor_Run_InfraError(t *testing.T) {
	// daemon 未启动：退出码 125 + stderr 命中特征短语 → ErrDockerUnavailable。
	rec := &fakeDockerRecorder{
		runHook: func(args []string) (string, string, int, error) {
			return "", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", 125, nil
		},
	}
	d := newTestDocker(t, DockerOptions{}, rec)
	_, err := d.Run(context.Background(), "ls")
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v, want ErrDockerUnavailable", err)
	}
}

func TestDockerExecutor_Run_NonZeroExitNotInfra(t *testing.T) {
	// 容器内命令真实非零退出（如 ls 不存在）→ 有效结果，不误判为基础设施故障。
	rec := &fakeDockerRecorder{
		runHook: func(args []string) (string, string, int, error) {
			return "", "ls: /no/such/file: No such file or directory", 2, nil
		},
	}
	d := newTestDocker(t, DockerOptions{}, rec)
	res, err := d.Run(context.Background(), "ls /no/such/file")
	if err != nil {
		t.Fatalf("Run: %v（容器内命令非零退出不应报错）", err)
	}
	if res.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", res.ExitCode)
	}
}

func TestDockerExecutor_Run_TimeoutCleansContainer(t *testing.T) {
	rec := &fakeDockerRecorder{
		runHook: func(args []string) (string, string, int, error) {
			// 模拟 docker run 因超时被杀（runner 返回 ctx 超时错误）。
			return "", "", -1, context.DeadlineExceeded
		},
	}
	d := newTestDocker(t, DockerOptions{Timeout: 10 * time.Millisecond}, rec)
	// 手工把 cidfile 内容预写，验证清理路径：直接调 cleanupContainer。
	cid := filepath.Join(t.TempDir(), "cid")
	if err := os.WriteFile(cid, []byte("abc123container\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.cleanupContainer(cid, "docker")
	// cleanupContainer 的 runner 收到 "docker rm -f abc123container"（exit 125 属失败，
	// 仅记日志不报错）——断言调用记录中存在 rm 即可。
	found := false
	for _, call := range rec.calls {
		if len(call) >= 4 && call[1] == "rm" && call[2] == "-f" && call[3] == "abc123container" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("超时清理未调用 docker rm -f，calls = %v", rec.calls)
	}
}

func TestDockerExecutor_Check(t *testing.T) {
	rec := &fakeDockerRecorder{version: "27.0.0"}
	d := newTestDocker(t, DockerOptions{}, rec)
	if err := d.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestDockerExecutor_WorkdirAndDefaults(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDockerExecutor(dir, DockerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Workdir() != dir {
		t.Fatalf("Workdir() = %q, want %q", d.Workdir(), dir)
	}
	if d.opts.Image != "alpine:3.20" || d.opts.Network != "none" || !d.opts.readOnly() || d.opts.Bin != "docker" {
		t.Fatalf("默认值回落错误: %+v", d.opts)
	}
}

func TestDockerExecutor_ReadOnlyOff(t *testing.T) {
	// 显式 ReadOnly=false 时不应拼 --read-only。
	ro := false
	rec := &fakeDockerRecorder{}
	d := newTestDocker(t, DockerOptions{ReadOnly: &ro}, rec)
	if _, err := d.Run(context.Background(), "echo hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(strings.Join(rec.lastRun(), " "), "--read-only") {
		t.Fatalf("ReadOnly=false 时不应出现 --read-only：%v", rec.lastRun())
	}
}

func TestNewBackendExecutor(t *testing.T) {
	dir := t.TempDir()
	// host（含空值/非法值）→ HostExecutor
	for _, b := range []Backend{"", "host", "invalid-xyz"} {
		ex, err := NewBackendExecutor(b, dir, DockerOptions{})
		if err != nil {
			t.Fatalf("NewBackendExecutor(%q): %v", b, err)
		}
		if _, ok := ex.(*HostExecutor); !ok {
			t.Fatalf("backend %q: 得到 %T, want *HostExecutor", b, ex)
		}
	}
	// docker → DockerExecutor
	ex, err := NewBackendExecutor(BackendDocker, dir, DockerOptions{})
	if err != nil {
		t.Fatalf("NewBackendExecutor(docker): %v", err)
	}
	if _, ok := ex.(*DockerExecutor); !ok {
		t.Fatalf("backend docker: 得到 %T, want *DockerExecutor", ex)
	}
}
