package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Spec {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q) 失败: %v", expr, err)
	}
	return s
}

func TestParse_Errors(t *testing.T) {
	cases := []string{
		"", "*/1 * * *", "* * * * * *", // 字段数不对
		"70 * * * *", // 分钟越界
		"* 24 * * *", // 小时越界
		"* * 32 * *", // 日越界
		"* * * 13 *", // 月越界
		"* * * * 8",  // 周越界（>7）
		"*/0 * * * *", // 步长 0
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) 应报错但未报", c)
		}
	}
}

func TestNext_EveryMinute(t *testing.T) {
	s := mustParse(t, "*/1 * * * *")
	from := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	got, err := s.Next(from)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 10, 10, 1, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", from, got, want)
	}
}

func TestNext_HourlyAtZero(t *testing.T) {
	s := mustParse(t, "0 * * * *")
	from := time.Date(2026, 8, 10, 10, 30, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_DailyAt0900(t *testing.T) {
	s := mustParse(t, "0 9 * * *")
	from := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_SundayMidnight(t *testing.T) {
	s := mustParse(t, "0 0 * * 0")
	// 2026-08-10 是 Monday，下一个周日应是 2026-08-16。
	from := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_MonthlyFirst(t *testing.T) {
	s := mustParse(t, "0 0 1 * *")
	from := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_DOMOrDOW(t *testing.T) {
	// 每月 13 号 或 每周五 0 点触发。2026-08-01 是周六，下一个周五(08-07)先于 13 号命中。
	s := mustParse(t, "0 0 13 * 5")
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_StepAndList(t *testing.T) {
	// 每 15 分钟的 0/15/30/45 分触发。
	s := mustParse(t, "0/15 * * * *")
	from := time.Date(2026, 8, 10, 10, 7, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 10, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestNext_StrictlyAfter(t *testing.T) {
	// 当 from 恰好命中表达式时，Next 应返回下一个（严格大于）。
	s := mustParse(t, "0 9 * * *")
	from := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	got, _ := s.Next(from)
	want := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next 应严格大于 from, got %v", got)
	}
}
