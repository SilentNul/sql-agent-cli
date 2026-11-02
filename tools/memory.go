package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
)

// memoryStore 用 JSON 文件持久化跨会话记忆（key -> content）。
type memoryStore struct {
	path string
	mu   sync.Mutex
}

func (s *memoryStore) load() (map[string]string, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	m := map[string]string{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("记忆文件 %s 解析失败: %w", s.path, err)
	}
	return m, nil
}

func (s *memoryStore) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func (s *memoryStore) write(ctx context.Context, args map[string]any) (string, error) {
	key, _ := args["key"].(string)
	content, _ := args["content"].(string)
	key = strings.TrimSpace(key)
	content = strings.TrimSpace(content)
	if key == "" || content == "" {
		return "", fmt.Errorf("参数 key 和 content 必填且不能为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return "", err
	}
	m[key] = content
	if err := s.save(m); err != nil {
		return "", err
	}
	return fmt.Sprintf("已记住：%s = %s", key, content), nil
}

func (s *memoryStore) read(ctx context.Context, args map[string]any) (string, error) {
	key, _ := args["key"].(string)
	key = strings.TrimSpace(key)

	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return "", err
	}

	if key != "" {
		v, ok := m[key]
		if !ok {
			return fmt.Sprintf("未找到记忆 %q", key), nil
		}
		return fmt.Sprintf("%s = %s", key, v), nil
	}
	if len(m) == 0 {
		return "（暂无记忆）", nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, m[k])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// NewMemoryTools 返回跨会话记忆的写入/读取工具，两者共享同一把锁和同一个文件。
func NewMemoryTools(path string) (write *ToolDef, read *ToolDef) {
	store := &memoryStore{path: path}

	write = &ToolDef{
		Name:        "memory_write",
		Description: "把一条需要长期记住的信息写入本地记忆文件（跨会话保留）。当用户说\"记住 / 以后都用 / 我的偏好是……\"时调用。key 用简短主题名，content 写具体内容；相同 key 会覆盖旧值。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]string{
					"type":        "string",
					"description": "记忆的主题/键名，简短且唯一，例如 '用户姓名'、'默认数据库'、'常用统计口径'",
				},
				"content": map[string]string{
					"type":        "string",
					"description": "要记住的具体内容",
				},
			},
			"required": []string{"key", "content"},
		},
		Fn: store.write,
	}

	read = &ToolDef{
		Name:        "memory_read",
		Description: "读取本地记忆。不传 key 时返回全部记忆；传 key 时返回该条记忆。当需要了解用户的长期偏好或之前交代过的事情时调用。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]string{
					"type":        "string",
					"description": "可选，要读取的记忆键名；不传则返回全部",
				},
			},
		},
		Fn: store.read,
	}

	return write, read
}
