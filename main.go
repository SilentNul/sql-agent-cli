package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"agent/core"
	"agent/mcp"
	"agent/tools"
	"agent/ui"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

const MaxLoop = 40

// MCP 配置文件路径（相对运行目录）
const mcpConfigFile = "mcp.json"

const (
	// 模型名：换成任意 OpenAI 兼容服务的模型即可。
	// 例如 OpenAI 用 "gpt-4o-mini"，DeepSeek 官方用 "deepseek-chat"。
	LLMModel = "deepseek-ai/DeepSeek-V4-Flash"

	// API 地址：换成你的服务商即可。
	// OpenAI 官方 "https://api.openai.com/v1"，DeepSeek 官方 "https://api.deepseek.com/v1"。
	LLMBaseURL = "https://api.siliconflow.cn/v1"
)

// 生成工具列表
func buildOpenaiTools(m map[string]*tools.ToolDef) []openai.ChatCompletionToolUnionParam {
	openaiTools := make([]openai.ChatCompletionToolUnionParam, 0)
	for _, tool := range m {
		openaiTools = append(openaiTools, tool.ToChatParam())
	}
	return openaiTools
}

// 判断用户这句话是否表示"同意执行"。
func isAffirmative(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	for _, n := range []string{"不要", "不用", "取消", "算了", "别执行", "否", "no", "cancel"} {
		if strings.Contains(t, n) {
			return false
		}
	}
	for _, p := range []string{"确认", "可以", "同意", "执行", "好的", "ok", "yes", "确定"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	switch t {
	case "是", "y", "嗯", "对", "执行吧":
		return true
	}
	return false
}

// summarizeOld 调用模型，把一段历史消息压缩成摘要文本（策略 B 的兜底）。
func summarizeOld(ctx context.Context, client *openai.Client, old []openai.ChatCompletionMessageParamUnion) (string, error) {
	b, err := json.Marshal(old)
	if err != nil {
		return "", err
	}
	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: LLMModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("你是对话摘要器。把用户给出的历史对话压缩成一段简洁摘要，保留关键事实、已完成的步骤、结论和未决问题，只输出摘要文本。"),
			openai.UserMessage("请把以下历史对话（JSON 格式）压缩成一段摘要：\n" + string(b)),
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("摘要生成没有返回内容")
	}
	return resp.Choices[0].Message.Content, nil
}

// 执行问答
func runTurn(ctx context.Context, client *openai.Client, session *core.Session, llmTools map[string]*tools.ToolDef, openaiTools []openai.ChatCompletionToolUnionParam) (string, error) {
	lastSig := ""    //记录上次调用
	repeatCount := 0 //重复次数

	for loop := 0; loop < MaxLoop; loop++ { //调用LLM
		// 上下文逼近窗口上限时，先清理较早的工具结果
		session.MaybeCompact()
		// 清理后仍逼近上限，则把更早的对话摘要成一段文字替换（兜底）
		if session.NeedsSummary() {
			if old := session.OldForSummary(); len(old) > 0 {
				if sum, sErr := summarizeOld(ctx, client, old); sErr == nil && strings.TrimSpace(sum) != "" {
					session.ApplySummary(sum)
				}
			}
		}

		// 把当前计划临时附加到本次请求（不写入 session，避免累积）
		msgs := session.GetMessage()
		if plan := session.RenderTodos(); plan != "" {
			msgs = append([]openai.ChatCompletionMessageParamUnion{}, msgs...)
			msgs = append(msgs, openai.SystemMessage("当前任务计划（完成一步后请用 todo_write 更新状态）：\n"+plan))
		}

		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: msgs,
			Tools:    openaiTools,
			Model:    LLMModel,
		})
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("模型没有返回任何回复")
		}
		msg := resp.Choices[0].Message
		// 模型没有发起工具调用
		if len(msg.ToolCalls) == 0 {
			session.AddMessage(openai.AssistantMessage(msg.Content))
			return msg.Content, nil
		}

		// 让模型下一轮知道自己做过什么
		toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			toolCallParams = append(toolCallParams, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: tc.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				},
			})
		}
		session.AddMessage(openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content:   openai.ChatCompletionAssistantMessageParamContentUnion{OfString: param.NewOpt(msg.Content)},
				ToolCalls: toolCallParams,
			},
		})

		// 逐个执行工具调用
		for _, tc := range msg.ToolCalls {
			toolRe := ""
			if v, ok := llmTools[tc.Function.Name]; ok { //查找工具
				toolRe, err = v.Call(ctx, tc.Function.Arguments)
				if err != nil {
					toolRe = fmt.Sprintf("工具 %s 执行出错: %v", tc.Function.Name, err)
				}
			} else {
				toolRe = fmt.Sprintf("未知工具: %s", tc.Function.Name)
			}

			// 防止太长
			if len(toolRe) > 2000 {
				toolRe = toolRe[:2000] + "...(已截断)"
			}

			// 同一工具同一参数被连续重复调用时提前中止，
			sig := tc.Function.Name + "|" + tc.Function.Arguments
			if sig == lastSig {
				repeatCount++
			} else {
				lastSig = sig
				repeatCount = 1
			}
			if repeatCount >= 3 {
				return "", fmt.Errorf("检测到对 %s 的重复调用（相同参数连续 %d 次），已中止。最后一次返回: %s",
					tc.Function.Name, repeatCount, toolRe)
			}

			session.AddMessage(openai.ToolMessage(toolRe, tc.ID))
		}
	}

	return "", fmt.Errorf("达到最大循环次数 %d，仍未得到最终回答", MaxLoop)
}

func main() {
	// 从环境变量读取 LLM API Key（避免硬编码泄漏）
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		panic("未设置环境变量 LLM_API_KEY，请先设置你的 API Key，例如：$env:LLM_API_KEY=\"sk-...\"")
	}

	// 连接 MySQL
	db, err := tools.InitDB()
	if err != nil {
		panic("数据库连接失败，请检查环境变量 MYSQL_DSN 是否正确: " + err.Error())
	}
	defer db.Close()

	// 内置 SQL 工具（固定存在）
	llmTools := map[string]*tools.ToolDef{}
	schemaTool := tools.NewGetTableSchema(db)
	execTool := tools.NewExecuteSQL(db)
	sampleTool := tools.NewGetSampleData(db)
	llmTools[schemaTool.Name] = schemaTool
	llmTools[execTool.Name] = execTool
	llmTools[sampleTool.Name] = sampleTool

	// 跨会话长记忆：写入/读取本地记忆文件
	memoryWriteTool, memoryReadTool := tools.NewMemoryTools("agent_memory.json")
	llmTools[memoryWriteTool.Name] = memoryWriteTool
	llmTools[memoryReadTool.Name] = memoryReadTool

	// MCP 管理：mcpMgr 持有所有 server 连接
	mcpMgr := mcp.NewManager()
	defer mcpMgr.Close()

	// 记录当前已挂载的 MCP 工具名，reload/remove 时据此增删
	mcpToolNames := map[string]bool{}
	syncMCP := func() {
		for n := range mcpToolNames {
			delete(llmTools, n)
		}
		mcpToolNames = map[string]bool{}
		for n, td := range mcpMgr.ToolDefs() {
			llmTools[n] = td
			mcpToolNames[n] = true
		}
	}

	// 启动时加载 mcp.json 里声明的 server
	startCtx, cancelStart := context.WithTimeout(context.Background(), 60*time.Second)
	if err := mcpMgr.LoadFile(startCtx, mcpConfigFile); err != nil {
		fmt.Printf("警告: 加载 MCP 配置失败: %v\n", err)
	}
	cancelStart()
	syncMCP()

	// 初始化session，写入Agent角色
	session := core.NewSession()
	session.AddMessage(openai.SystemMessage(
		`你是一个数据分析助手，帮用户用自然语言查询和修改 MySQL 数据库。
		注意：
		删除字段用 ALTER TABLE ... DROP COLUMN（允许，需用户确认）；
		删除数据行用 DELETE ... WHERE（允许，需用户确认，且必须带 WHERE）；
		仅 DROP TABLE / DROP DATABASE / TRUNCATE 这类整表级破坏操作永远被禁止；DELETE/UPDATE 必须包含 WHERE 子句；
		影响超过 100 行的写操作会被拒绝。
			需要记住用户的长期偏好时用 memory_write；需要回忆之前交代过的事情时用 memory_read。
			遇到需要多步操作的任务时，先用 todo_write 列出计划，完成一步后更新对应步骤的 done 状态。`,
	))

	// 规划工具（计划保存在 session 中，跨轮保留）
	todoWriteTool := tools.NewTodoWrite(session)
	todoReadTool := tools.NewTodoRead(session)
	llmTools[todoWriteTool.Name] = todoWriteTool
	llmTools[todoReadTool.Name] = todoReadTool

	// 创建LLM客户端
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(LLMBaseURL),
		option.WithRequestTimeout(60*time.Second),
	)

	// 处理终端里输入的 MCP 命令（/mcp ...）
	runCommand := func(ctx context.Context, input string) (string, bool) {
		tokens := strings.Fields(input)
		if len(tokens) == 0 || tokens[0] != "/mcp" {
			return "", false
		}
		cmd := ""
		if len(tokens) >= 2 {
			cmd = tokens[1]
		}

		switch cmd {
		case "", "list", "ls":
			servers := mcpMgr.Servers()
			if len(servers) == 0 {
				return "当前没有连接的 MCP server。用 /mcp add <name> <command> [args...] 添加，或在 mcp.json 里配置后用 /mcp reload 加载。", true
			}
			toolMap := mcpMgr.ToolList()
			var b strings.Builder
			b.WriteString("已连接的 MCP server：\n")
			for _, s := range servers {
				b.WriteString(fmt.Sprintf("\n· %s（%d 个工具）\n", s, len(toolMap[s])))
				for _, t := range toolMap[s] {
					b.WriteString("    - ")
					b.WriteString(t)
					b.WriteString("\n")
				}
			}
			return strings.TrimRight(b.String(), "\n"), true

		case "add":
			if len(tokens) < 4 {
				return "用法: /mcp add <name> <command> [args...]  或  /mcp add <name> <http-url>", true
			}
			name := tokens[2]
			rest := tokens[3:]
			cfg := mcp.ServerConfig{Name: name}
			if len(rest) == 1 && (strings.HasPrefix(rest[0], "http://") || strings.HasPrefix(rest[0], "https://")) {
				cfg.Transport = "sse"
				cfg.URL = rest[0]
			} else {
				cfg.Transport = "stdio"
				cfg.Command = rest[0]
				cfg.Args = rest[1:]
			}
			if err := mcpMgr.Connect(ctx, cfg); err != nil {
				return fmt.Sprintf("连接 %s 失败: %v", name, err), true
			}
			syncMCP()
			toolMap := mcpMgr.ToolList()
			return fmt.Sprintf("已连接 %s，注册 %d 个工具", name, len(toolMap[name])), true

		case "remove", "rm":
			if len(tokens) < 3 {
				return "用法: /mcp remove <name>", true
			}
			name := tokens[2]
			mcpMgr.Disconnect(name)
			syncMCP()
			return "已断开 " + name, true

		case "reload":
			if err := mcpMgr.Reload(ctx, mcpConfigFile); err != nil {
				return "重新加载失败: " + err.Error(), true
			}
			syncMCP()
			return "已从 " + mcpConfigFile + " 重新加载", true

		case "help":
			return "MCP 命令：\n  /mcp list          查看已连接的 server 和工具\n  /mcp add <name> <command> [args...]  添加 stdio server\n  /mcp add <name> <http-url>  添加远程 SSE server\n  /mcp remove <name>  断开 server\n  /mcp reload        重新加载 mcp.json", true

		default:
			return fmt.Sprintf("未知 MCP 命令: %q，输入 /mcp help 查看用法", cmd), true
		}
	}

	//启动Bubble Tea UI
	if err := ui.Run(ui.Deps{
		Ask: func(ctx context.Context, input string) (string, error) {
			// 终端命令优先，不走 LLM
			if out, handled := runCommand(ctx, input); handled {
				return out, nil
			}

			// 写操作审批
			if isAffirmative(input) {
				tools.ArmWriteApproval()
			} else {
				tools.CancelWriteApproval()
			}

			// 把用户问题存进session
			session.AddMessage(openai.UserMessage(input))
			// 调 runTurn：每轮按最新工具集重建一次，再进入LLM循环
			return runTurn(ctx, &client, session, llmTools, buildOpenaiTools(llmTools))
		},
	}); err != nil {
		fmt.Println("程序运行失败:", err)
	}
}
