package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Conn 封装一个 MCP server 的连接（stdio 或 sse）。
type Conn struct {
	Name  string
	cfg   ServerConfig
	cli   *mcpclient.Client
	tools []mcp.Tool
}

func newConn(cfg ServerConfig) *Conn {
	return &Conn{Name: cfg.Name, cfg: cfg}
}

// Connect 建立连接、完成 MCP 握手并拉取工具列表。
func (c *Conn) Connect(ctx context.Context) error {
	var (
		cli *mcpclient.Client
		err error
	)
	switch strings.ToLower(strings.TrimSpace(c.cfg.Transport)) {
	case "", "stdio":
		cli, err = mcpclient.NewStdioMCPClient(c.cfg.Command, c.cfg.Env, c.cfg.Args...)
	case "sse", "http":
		cli, err = mcpclient.NewSSEMCPClient(c.cfg.URL)
	default:
		return fmt.Errorf("不支持的 transport: %q（支持 stdio / sse）", c.cfg.Transport)
	}
	if err != nil {
		return err
	}
	c.cli = cli

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "agent", Version: "1.0.0"}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		cli.Close()
		return fmt.Errorf("初始化 MCP 握手失败: %w", err)
	}

	tools, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		cli.Close()
		return fmt.Errorf("拉取工具列表失败: %w", err)
	}
	c.tools = tools.Tools
	return nil
}

// CallTool 调用 MCP 工具，并把结果整理成纯文本返回。
func (c *Conn) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.cli.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	return toolResultToText(result), nil
}

// Close 关闭连接，会顺带结束 stdio 子进程。
func (c *Conn) Close() {
	if c.cli != nil {
		c.cli.Close()
		c.cli = nil
	}
}

// toolResultToText 把 MCP 工具返回结果转成字符串。
func toolResultToText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}

	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		parts = append(parts, mcp.GetTextFromContent(content))
	}

	// 部分工具只返回结构化内容，没有纯文本 block。
	if len(parts) == 0 && result.StructuredContent != nil {
		if b, err := json.Marshal(result.StructuredContent); err == nil {
			return string(b)
		}
	}

	return strings.Join(parts, "\n")
}
