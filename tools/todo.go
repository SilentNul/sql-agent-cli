package tools

import (
	"context"
	"fmt"
	"strings"

	"agent/core"

	"github.com/openai/openai-go/v3"
)

// NewTodoWrite 创建/更新执行计划。计划保存在 Session 中（会话级状态）。
func NewTodoWrite(session *core.Session) *ToolDef {
	return &ToolDef{
		Name:        "todo_write",
		Description: "为复杂任务创建或更新执行计划（todo 列表）。当任务需要多步操作时，先调用它列出步骤；每完成一步再调用一次更新 done 状态。参数 todos 会整体替换当前计划。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "完整的计划步骤列表（整体替换当前计划）",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"step": map[string]string{
								"type":        "string",
								"description": "这一步要做什么",
							},
							"done": map[string]string{
								"type":        "boolean",
								"description": "这一步是否已完成",
							},
						},
						"required": []string{"step", "done"},
					},
				},
			},
			"required": []string{"todos"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			raw, ok := args["todos"].([]any)
			if !ok {
				return "", fmt.Errorf("参数 todos 必须是数组")
			}
			todos := make([]core.Todo, 0, len(raw))
			for i, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					return "", fmt.Errorf("todos[%d] 必须是对象", i)
				}
				step, _ := m["step"].(string)
				step = strings.TrimSpace(step)
				if step == "" {
					return "", fmt.Errorf("todos[%d].step 不能为空", i)
				}
				done, _ := m["done"].(bool)
				todos = append(todos, core.Todo{Step: step, Done: done})
			}
			if len(todos) == 0 {
				return "", fmt.Errorf("todos 不能为空")
			}
			session.SetTodos(todos)
			return "计划已更新：\n" + session.RenderTodos(), nil
		},
	}
}

// NewTodoRead 读取当前计划与完成状态。
func NewTodoRead(session *core.Session) *ToolDef {
	return &ToolDef{
		Name:        "todo_read",
		Description: "读取当前任务计划与各步骤的完成状态。",
		Params: openai.FunctionParameters{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			if len(session.GetTodos()) == 0 {
				return "（当前没有计划）", nil
			}
			return session.RenderTodos(), nil
		},
	}
}
