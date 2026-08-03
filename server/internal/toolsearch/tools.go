package toolsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ToolSearchName 是检索工具在 Agent 侧的注册名（控制工具之一）。
const ToolSearchName = "tool_search"

// ToolCallName 是按需调用工具在 Agent 侧的注册名（控制工具之二）。
const ToolCallName = "call_tool"

// searchInput 是 tool_search 的输入：名称/关键字 + 数量上限。
type searchInput struct {
	Query string `json:"query" description:"名称或关键字，用于在延迟工具箱里模糊匹配工具；留空则返回全部工具"`
	Limit int    `json:"limit" description:"返回工具数量上限；<=0 表示不限制"`
}

// searchResultItem 是 tool_search 返回的单个匹配项。
type searchResultItem struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Description string `json:"description"`
}

// NewToolSearch 返回一个名为 "tool_search" 的函数工具：在工具箱里按关键字检索工具，
// 返回匹配工具的「完整命名 + 描述」，供模型据此用 call_tool 调用。
// 工具箱里的真实工具（如 MCP 工具）默认不暴露给模型，只有被检索命中后才按需调用，
// 从而避免上下文 token 随工具数量线性膨胀。
func NewToolSearch(tb *Toolbox) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in searchInput) (string, error) {
			if tb == nil {
				return "[]", nil
			}
			hits := tb.Search(in.Query, in.Limit)
			items := make([]searchResultItem, 0, len(hits))
			for _, h := range hits {
				items = append(items, searchResultItem{
					Name:        h.Name,
					Namespace:   h.Namespace,
					Description: h.Description,
				})
			}
			b, err := json.Marshal(items)
			if err != nil {
				return "", fmt.Errorf("tool_search: 序列化结果失败: %w", err)
			}
			return string(b), nil
		},
		function.WithName(ToolSearchName),
		function.WithDescription("在延迟工具箱里按名称或关键字检索可用工具（例如 MCP 服务器提供的工具）。"+
			"返回匹配工具的完整命名（形如 mcp__<服务器>__<工具>）与描述；随后用 call_tool 按该命名调用。"+
			"默认不会把所有工具一次性暴露给模型，请先检索再调用，避免上下文膨胀。"),
	)
}

// callInput 是 call_tool 的输入：完整命名 + JSON 参数字符串。
type callInput struct {
	Name      string `json:"name" description:"要调用的工具完整命名（含命名空间，如 mcp__<服务器>__<工具>），来自 tool_search 的返回"`
	Arguments string `json:"arguments" description:"工具的 JSON 参数字符串；无参数可传 \"{}\""`
}

// NewCallTool 返回一个名为 "call_tool" 的函数工具：按完整命名从工具箱取出真实工具并执行，
// 把结果（JSON 字符串）返回给模型。工具本身不常驻 Agent 上下文，仅在被检索命中后才按需调用。
func NewCallTool(tb *Toolbox) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in callInput) (string, error) {
			if tb == nil {
				return "", fmt.Errorf("call_tool: 工具箱未初始化")
			}
			t, ok := tb.Get(in.Name)
			if !ok {
				return "", fmt.Errorf("call_tool: 未找到名为 %q 的工具（请先用 tool_search 检索）", in.Name)
			}
			ct, ok := t.(tool.CallableTool)
			if !ok {
				return "", fmt.Errorf("call_tool: 工具 %q 不可调用", in.Name)
			}
			args := in.Arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			result, err := ct.Call(ctx, []byte(args))
			if err != nil {
				return "", fmt.Errorf("call_tool: 调用 %q 失败: %w", in.Name, err)
			}
			return resultToString(result), nil
		},
		function.WithName(ToolCallName),
		function.WithDescription("按完整命名（来自 tool_search）从延迟工具箱取出真实工具并执行，返回结果字符串。"+
			"用于按需调用检索到的 MCP 工具或内置工具，避免一次性把所有工具暴露给模型导致上下文膨胀。"),
	)
}

// resultToString 把工具返回结果统一转为可读字符串（优先 JSON，失败退回 %v）。
func resultToString(result any) string {
	if result == nil {
		return ""
	}
	if s, ok := result.(string); ok {
		return s
	}
	if b, err := json.Marshal(result); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", result)
}
