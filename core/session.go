package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
)

const (
	// 上下文窗口上限（token），按所用模型的窗口调整。
	ContextWindowTokens = 128_000
	// 达到窗口的 80% 时触发压缩，避免压得太频繁破坏缓存。
	CompactTriggerRatio = 0.8
	// 压缩时保留最近的消息条数（大致等于最近几轮），更早的只清理工具结果。
	KeepRecentMessages = 12
)

// Todo 是计划中的一步。
type Todo struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

type Session struct {
	Messages []openai.ChatCompletionMessageParamUnion
	Todos    []Todo
}

// 初始化
func NewSession() *Session {
	return &Session{
		Messages: []openai.ChatCompletionMessageParamUnion{},
	}
}

// 获取消息内容
func (s *Session) GetMessage() []openai.ChatCompletionMessageParamUnion {
	return s.Messages
}

// 添加消息
func (s *Session) AddMessage(messages ...openai.ChatCompletionMessageParamUnion) {
	s.Messages = append(s.Messages, messages...)
}

// estimateTokens 粗略估算 messages 的 token 数。
// json.Marshal 后按字节数除以 3 近似：中文约 1 token/字（3 字节），英文约 4 字符/token。
func estimateTokens(msgs []openai.ChatCompletionMessageParamUnion) int {
	b, err := json.Marshal(msgs)
	if err != nil {
		return 0
	}
	return len(b) / 3
}

// MaybeCompact 在上下文逼近窗口上限时执行一次压缩，返回是否发生了压缩。
func (s *Session) MaybeCompact() bool {
	if estimateTokens(s.Messages) < int(float64(ContextWindowTokens)*CompactTriggerRatio) {
		return false
	}
	return s.Compact()
}

// Compact 采用"清理旧工具结果"策略：保留 system 与最近的消息，
// 把更早的工具输出替换成占位符（保持 tool_call_id 配对，避免 API 报错）。
func (s *Session) Compact() bool {
	if len(s.Messages) <= KeepRecentMessages+1 {
		return false
	}
	cutoff := len(s.Messages) - KeepRecentMessages
	changed := false
	// 从索引 1 开始，保留索引 0 的 system 消息
	for i := 1; i < cutoff; i++ {
		if m := s.Messages[i].OfTool; m != nil {
			s.Messages[i] = openai.ToolMessage("[较早的工具结果已清理以压缩上下文]", m.ToolCallID)
			changed = true
		}
	}
	return changed
}

// NeedsSummary 判断清理旧工具结果后是否仍逼近窗口上限，需要进一步做摘要压缩。
func (s *Session) NeedsSummary() bool {
	return estimateTokens(s.Messages) >= int(float64(ContextWindowTokens)*CompactTriggerRatio)
}

// safeCutoff 计算可安全摘要的切分点：删除区为 [1, cutoff)，保留区为 [cutoff, end)。
// 保证保留区不以工具消息开头，避免出现孤立的 tool_result 导致 API 报错。
func (s *Session) safeCutoff() int {
	cutoff := len(s.Messages) - KeepRecentMessages
	if cutoff <= 1 {
		return -1
	}
	for cutoff > 1 && s.Messages[cutoff].OfTool != nil {
		cutoff--
	}
	if cutoff <= 1 {
		return -1
	}
	return cutoff
}

// OldForSummary 返回准备被摘要替换的早期消息（不含 system 与最近消息）。
func (s *Session) OldForSummary() []openai.ChatCompletionMessageParamUnion {
	cutoff := s.safeCutoff()
	if cutoff < 0 {
		return nil
	}
	return s.Messages[1:cutoff]
}

// ApplySummary 用一段摘要替换早期的消息，保留 system 与最近消息。
func (s *Session) ApplySummary(summary string) bool {
	cutoff := s.safeCutoff()
	if cutoff < 0 {
		return false
	}
	newMsgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(s.Messages)-cutoff+2)
	newMsgs = append(newMsgs, s.Messages[0]) // 保留 system
	newMsgs = append(newMsgs, openai.UserMessage("【此前对话摘要】\n"+summary))
	newMsgs = append(newMsgs, s.Messages[cutoff:]...)
	s.Messages = newMsgs
	return true
}

// SetTodos 整体替换当前计划。
func (s *Session) SetTodos(todos []Todo) {
	s.Todos = todos
}

// GetTodos 返回当前计划。
func (s *Session) GetTodos() []Todo {
	return s.Todos
}

// RenderTodos 把计划渲染成文本清单；没有计划时返回空串。
func (s *Session) RenderTodos() string {
	var b strings.Builder
	for i, t := range s.Todos {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, mark, t.Step)
	}
	return strings.TrimRight(b.String(), "\n")
}
