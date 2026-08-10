// Package cron 实现标准 5 字段 cron 表达式解析与「下一次触发时间」计算（M4-02 调度器依赖）。
//
// 字段顺序：分 时 日 月 周（与 Unix crontab 一致），各字段支持：
//   - `*`        任意值
//   - `*/n`      从下界起按步长 n 取值（如 */5）
//   - `a`        单值
//   - `a-b`      闭区间范围
//   - `a-b/n`    范围内按步长
//   - 逗号列表组合以上（如 1,3,5 或 0-30/10,45）
// 月与周支持三字母英文名（jan..dec / sun..sat）。周字段 7 视为 Sunday（归一化为 0）。
// 日（dom）与周（dow）的语义遵循 Vixie cron 规则：两者之一为 `*` 时由另一个约束；
// 两者均受限（非 `*`）时，某一日满足「dom 命中 或 dow 命中」即触发（OR 语义）。
//
// 设计上零第三方依赖，保持 `go build` 在离线/沙箱环境也能通过。
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// matcher 表示某一字段的允许取值集合。
type matcher struct {
	all bool
	set map[int]bool
}

// match 判断值 v 是否落在允许集合内（`all` 时恒真）。
func (m matcher) match(v int) bool {
	if m.all {
		return true
	}
	return m.set[v]
}

// Spec 是解析后的 cron 调度。
type Spec struct {
	minute matcher
	hour   matcher
	dom    matcher
	month  matcher
	dow    matcher
}

// monthNames / dowNames 提供月与周的三字母别名映射（小写）。
var (
	monthNames = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	dowNames = map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}
)

// Parse 解析标准 5 字段 cron 表达式。
func Parse(expr string) (*Spec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("需要 5 个字段，得到 %d", len(fields))
	}
	s := &Spec{}
	var err error
	if s.minute, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if s.hour, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if s.dom, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	if s.month, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if s.dow, err = parseField(fields[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	// 周字段 7（Sunday）归一化为 0。
	if s.dow.set[7] {
		delete(s.dow.set, 7)
		s.dow.set[0] = true
	}
	return s, nil
}

// parseField 解析单个字段（可能含逗号分隔的多个子表达式）。
func parseField(field string, min, max int, names map[string]int) (matcher, error) {
	if field == "*" || field == "?" {
		return matcher{all: true}, nil
	}
	m := matcher{set: map[int]bool{}}
	for _, part := range strings.Split(field, ",") {
		if err := addPart(&m, strings.TrimSpace(part), min, max, names); err != nil {
			return matcher{}, err
		}
	}
	return m, nil
}

// addPart 解析一个子表达式（支持 */n、a、a-b、a-b/n、a/n）并写入 matcher。
// 注意 a/n 与 Vixie cron 一致：单值带步长表示从 a 一直取到该字段上界（如 0/15 → 0,15,30,45）。
func addPart(m *matcher, part string, min, max int, names map[string]int) error {
	if part == "" {
		return errors.New("空字段")
	}
	step := 1
	hasStep := false
	base := part
	if idx := strings.Index(part, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(strings.TrimSpace(part[idx+1:]))
		if err != nil || step <= 0 {
			return fmt.Errorf("无效步长 %q", part)
		}
		hasStep = true
		base = strings.TrimSpace(part[:idx])
	}

	var lo, hi int
	switch {
	case base == "*":
		lo, hi = min, max
	case strings.Contains(base, "-"):
		idx := strings.Index(base, "-")
		var err error
		lo, err = parseValue(base[:idx], min, max, names)
		if err != nil {
			return err
		}
		hi, err = parseValue(base[idx+1:], min, max, names)
		if err != nil {
			return err
		}
	default:
		v, err := parseValue(base, min, max, names)
		if err != nil {
			return err
		}
		lo = v
		if hasStep {
			hi = max // 单值带步长：从 v 取到上界
		} else {
			hi = v
		}
	}

	if lo > hi {
		return fmt.Errorf("范围无效 %q", part)
	}
	for v := lo; v <= hi; v += step {
		if v < min || v > max {
			return fmt.Errorf("值 %d 超出范围 [%d,%d]", v, min, max)
		}
		m.set[v] = true
	}
	return nil
}

// parseValue 解析单个数值，支持英文名。
func parseValue(s string, min, max int, names map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("无效值 %q", s)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("值 %d 超出范围 [%d,%d]", v, min, max)
	}
	return v, nil
}

// Next 返回 from 之后（严格大于）第一个匹配的时间（按分钟对齐）。
// 最多向后搜索 5 年；若仍无匹配返回 error（表达式空匹配集合的极端情况）。
func (s *Spec) Next(from time.Time) (time.Time, error) {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if s.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, errors.New("5 年内无匹配时间")
}

// matches 判定时间 t 是否命中全部字段（含 dom/dow 的 OR 规则）。
func (s *Spec) matches(t time.Time) bool {
	if !s.minute.match(t.Minute()) {
		return false
	}
	if !s.hour.match(t.Hour()) {
		return false
	}
	if !s.month.match(int(t.Month())) {
		return false
	}
	domMatch := s.dom.match(t.Day())
	dowMatch := s.dow.match(int(t.Weekday())) // time.Weekday: Sunday=0 .. Saturday=6
	domStar := s.dom.all
	dowStar := s.dow.all
	switch {
	case domStar && dowStar:
		return true // 两者皆 *：每天匹配
	case domStar:
		return dowMatch // 仅周约束
	case dowStar:
		return domMatch // 仅日约束
	default:
		return domMatch || dowMatch // 两者受限：OR
	}
}
