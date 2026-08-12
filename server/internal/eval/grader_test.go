package eval

import (
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

func TestGrade_Exact(t *testing.T) {
	cases := []struct {
		output   string
		expected string
		score    float64
		passed   bool
	}{
		{"2", "2", 1.0, true},
		{"  2  ", "2", 1.0, true},                  // 忽略首尾空白
		{"2\n", "2", 1.0, true},                   // 换行被裁掉
		{"3", "2", 0.0, false},                    // 不相等
		{"2 ", " 2", 1.0, true},                   // 双向空白忽略
		{"answer: 2", "2", 0.0, false},            // 非完全相等
	}
	for i, c := range cases {
		s, p := Grade(model.GraderExact, c.output, c.expected)
		if s != c.score || p != c.passed {
			t.Errorf("exact case %d: got (%.1f,%v) want (%.1f,%v)", i, s, p, c.score, c.passed)
		}
	}
}

func TestGrade_Contains(t *testing.T) {
	cases := []struct {
		output   string
		expected string
		passed   bool
	}{
		{"say hello world", "hello", true},       // 命中
		{"SAY HELLO WORLD", "hello", true},       // 大小写不敏感
		{"  hello  ", "hello", true},             // 期望空白忽略
		{"foo bar", "hello", false},              // 未命中
		{"", "hello", false},                     // 空输出
	}
	for i, c := range cases {
		s, p := Grade(model.GraderContains, c.output, c.expected)
		if p != c.passed {
			t.Errorf("contains case %d: got passed=%v want %v (score=%.1f)", i, p, c.passed, s)
		}
		if c.passed && s != 1.0 {
			t.Errorf("contains case %d: expected score 1.0 got %.1f", i, s)
		}
	}
}

func TestGrade_UnknownReturnsZero(t *testing.T) {
	s, p := Grade(model.GraderType("bogus"), "a", "a")
	if s != 0 || p {
		t.Errorf("unknown grader should return (0,false), got (%.1f,%v)", s, p)
	}
}
