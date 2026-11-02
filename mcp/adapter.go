package mcp

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go/v3"

	"agent/tools"
)

// toToolDef 把 MCP 工具转成 agent 的 tools.ToolDef。
// name 是最终暴露给 LLM 的工具名（已带 server 前缀），Fn 会转发回对应 server 的 CallTool。
func (c *Conn) toToolDef(name string, tool mcp.Tool) *tools.ToolDef {
	return &tools.ToolDef{
		Name:        name,
		Description: tool.Description,
		Params:      marshalParams(tool),
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			return c.CallTool(ctx, tool.Name, args)
		},
	}
}

// marshalParams 把 MCP 工具的 inputSchema 转成 OpenAI 的 FunctionParameters（JSON Schema）。
func marshalParams(tool mcp.Tool) openai.FunctionParameters {
	fallback := openai.FunctionParameters{
		"type":       "object",
		"properties": map[string]any{},
	}

	// RawInputSchema 优先级最高，否则用结构化 InputSchema。
	var (
		b   []byte
		err error
	)
	if len(tool.RawInputSchema) > 0 {
		b = tool.RawInputSchema
	} else {
		b, err = json.Marshal(tool.InputSchema)
		if err != nil {
			return fallback
		}
	}

	var p openai.FunctionParameters
	if err := json.Unmarshal(b, &p); err != nil || len(p) == 0 {
		return fallback
	}
	return p
}
