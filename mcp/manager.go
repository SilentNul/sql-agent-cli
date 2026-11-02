package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"agent/tools"
)

// ServerConfig 描述一个 MCP server，来自 mcp.json 的 "servers" 数组。
type ServerConfig struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport,omitempty"` // "stdio"（默认）或 "sse"
	Command   string   `json:"command,omitempty"`   // stdio 时：要启动的命令
	Args      []string `json:"args,omitempty"`      // stdio 时：命令参数
	Env       []string `json:"env,omitempty"`       // stdio 时：追加的环境变量 "KEY=value"
	URL       string   `json:"url,omitempty"`       // sse 时：远程服务地址
}

// Config 对应 mcp.json 的内容。
type Config struct {
	Servers []ServerConfig `json:"servers"`
}

// Manager 管理多个 MCP server 连接。
type Manager struct {
	mu    sync.RWMutex
	conns map[string]*Conn
}

func NewManager() *Manager {
	return &Manager{conns: map[string]*Conn{}}
}

// LoadFile 读取 JSON 配置并连接其中定义的所有 server。
// 单个 server 连接失败不会中止其它 server，最终汇总返回错误。
func (m *Manager) LoadFile(ctx context.Context, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}

	var errs []error
	for _, sc := range cfg.Servers {
		if err := m.Connect(ctx, sc); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sc.Name, err))
		}
	}
	return errors.Join(errs...)
}

// Reload 关闭现有全部连接，再按配置文件重新加载。
func (m *Manager) Reload(ctx context.Context, path string) error {
	m.Close()
	return m.LoadFile(ctx, path)
}

// Connect 连接（或重连）一个 MCP server。
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("server 缺少 name 字段")
	}
	c := newConn(cfg)
	if err := c.Connect(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	if old, ok := m.conns[cfg.Name]; ok {
		old.Close()
	}
	m.conns[cfg.Name] = c
	m.mu.Unlock()
	return nil
}

// Disconnect 断开并移除一个 server。
func (m *Manager) Disconnect(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[name]; ok {
		c.Close()
		delete(m.conns, name)
	}
}

// Close 关闭所有连接。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.Close()
	}
	m.conns = map[string]*Conn{}
}

// Servers 返回已连接 server 名（有序）。
func (m *Manager) Servers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.conns))
	for n := range m.conns {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ToolDefs 返回所有 MCP 工具合并后的 map。
// 工具名统一加 "server名_" 前缀，避免与内置工具或其它 server 冲突。
func (m *Manager) ToolDefs() map[string]*tools.ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]*tools.ToolDef)
	for _, c := range m.conns {
		for _, t := range c.tools {
			name := c.Name + "_" + t.Name
			out[name] = c.toToolDef(name, t)
		}
	}
	return out
}

// ToolList 返回每个 server 暴露的工具名（有序），便于在终端展示。
func (m *Manager) ToolList() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	serverNames := make([]string, 0, len(m.conns))
	for n := range m.conns {
		serverNames = append(serverNames, n)
	}
	sort.Strings(serverNames)

	out := make(map[string][]string, len(m.conns))
	for _, name := range serverNames {
		c := m.conns[name]
		toolNames := make([]string, 0, len(c.tools))
		for _, t := range c.tools {
			toolNames = append(toolNames, t.Name)
		}
		sort.Strings(toolNames)
		out[name] = toolNames
	}
	return out
}
