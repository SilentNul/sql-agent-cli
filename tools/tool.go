package tools

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

type ToolDef struct {
	Name        string
	Description string
	Params      openai.FunctionParameters
	Fn          func(ctx context.Context, args map[string]any) (string, error)
}

// 生成工具
func (t *ToolDef) ToChatParam() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  t.Params,
			},
		},
	}
}

// 把Json字符串转化为map发给Fn函数
func (t *ToolDef) Call(ctx context.Context, argsJSON string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	return t.Fn(ctx, args)
}
