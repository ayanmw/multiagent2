package codectool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileWrite_DiskQuotaBlocked 验收 M8-09 磁盘配额：写入前检查目录总大小，
// 超限拒绝（ErrDiskQuotaExceeded）且目标文件不落盘；配额内正常写入。
func TestFileWrite_DiskQuotaBlocked(t *testing.T) {
	dir := t.TempDir()
	const quota = int64(100)

	// 配额内写入成功。
	if out, err := FileWrite(dir, "ok.txt", "12345", quota); err != nil {
		t.Fatalf("配额内写入应成功: %v", err)
	} else if !strings.Contains(out, "ok.txt") {
		t.Fatalf("返回应包含文件名: %q", out)
	}

	// 超限写入被拒（目录已有 5 字节 + 本次 200 > 100）。
	_, err := FileWrite(dir, "big.txt", strings.Repeat("x", 200), quota)
	if err == nil || !errors.Is(err, ErrDiskQuotaExceeded) {
		t.Fatalf("超限应返回 ErrDiskQuotaExceeded, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "big.txt")); !os.IsNotExist(statErr) {
		t.Fatal("超限写入后目标文件不应存在")
	}

	// quota<=0 表示不限：大文件写入成功。
	if _, err := FileWrite(dir, "huge.txt", strings.Repeat("y", 500), 0); err != nil {
		t.Fatalf("quota=0 不限时应成功: %v", err)
	}
}

// TestFileEdit_DiskQuotaDelta 验收 file_edit 的净增量配额检查：
// 替换使文件变大超限 → 拒绝且文件不变；替换使文件变小 → 放行。
func TestFileEdit_DiskQuotaDelta(t *testing.T) {
	dir := t.TempDir()
	const quota = int64(250)
	if _, err := FileWrite(dir, "f.txt", "abc", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 替换增长 297 字节（3*99）→ 目录现有 3 + 297 > 250 拒绝。
	_, err := FileEdit(dir, "f.txt", "abc", strings.Repeat("y", 300), 1, quota)
	if err == nil || !errors.Is(err, ErrDiskQuotaExceeded) {
		t.Fatalf("增长超限应拒绝, got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(data) != "abc" {
		t.Fatalf("拒绝后文件应保持原样, got %q", string(data))
	}

	// 替换使文件变小（delta 负）→ 放行。
	if _, err := FileEdit(dir, "f.txt", "abc", "ab", 1, quota); err != nil {
		t.Fatalf("体积变小应放行: %v", err)
	}

	// 边界：目录现有 2 字节 + delta 2 = 4 <= 5 放行。
	if _, err := FileEdit(dir, "f.txt", "ab", "abcd", 1, 5); err != nil {
		t.Fatalf("边界内应放行: %v", err)
	}
	// 超边界：目录现有 4 字节 + delta 2 = 6 > 5 拒绝。
	if _, err := FileEdit(dir, "f.txt", "abcd", "abcdef", 1, 5); err == nil {
		t.Fatal("边界外应拒绝")
	}
}

// TestEnforceQuota_ZeroMeansUnlimited quota<=0 直接放行（不统计目录）。
func TestEnforceQuota_ZeroMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	if err := enforceQuota(dir, 0, 1<<30); err != nil {
		t.Fatalf("quota<=0 应放行, got %v", err)
	}
	if err := enforceQuota(dir, -5, 1<<30); err != nil {
		t.Fatalf("负 quota 应放行, got %v", err)
	}
}
