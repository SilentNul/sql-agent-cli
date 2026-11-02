# SQL Agent

一个用**自然语言操作 MySQL 数据库**的 AI 命令行 Agent。支持对话式查询/修改数据，并可通过 MCP（Model Context Protocol）灵活挂载外部工具（网页抓取、浏览器自动化、文件读写、时间查询等）来扩展能力。

## 功能特性

- **自然语言 → SQL**：直接说"帮我查最近5天北京的天气数据并整理成一个表格"，Agent 会自动了解表结构、抽样数据、生成并执行 SQL。
- **内置数据库工具**：`get_table_schema`、`get_sample_data`、`execute_sql` 三个开箱即用的 MySQL 工具。
- **写操作安全审批**：写操作不会直接执行，需用户在对话中再次确认后才真正执行；危险操作永远禁止（详见[安全机制](#安全机制)）。
- **MCP 工具扩展**：通过 `mcp.json` 挂载任意 MCP server（stdio 或 SSE），支持运行时增删/重载。
- **终端交互界面**：基于 Bubble Tea 的 TUI，支持鼠标滚动、Markdown 渲染、思考动画。
- **防死循环**：同一工具同一参数连续调用 3 次会自动中止，整体循环上限 40 轮。
- **上下文压缩**：上下文逼近窗口上限时，先清理较早的工具结果；仍超限则把更早的对话摘要成一段文字，避免长对话撑爆窗口。
- **跨会话长记忆**：通过 `memory_write` / `memory_read` 把用户的长期偏好存到本地 `agent_memory.json`，重启后依然有效。
- **任务规划**：复杂任务可先用 `todo_write` 列出步骤，执行中更新完成状态；当前计划每轮自动带入上下文，长任务更不容易跑偏。

## 目录结构

```
agent/
├── main.go                  # 入口：LLM 客户端、工具装配、对话循环、/mcp 命令处理
├── mcp.json                 # MCP server 配置（声明要连接哪些外部工具服务）
├── go.mod / go.sum
├── core/
│   └── session.go           # 对话历史管理与上下文压缩
├── tools/                   # 内置工具
│   ├── tool.go              # ToolDef 工具定义与调用封装
│   ├── db.go                # MySQL 连接、查询结果转 JSON、SQL 清理
│   ├── execute_sql.go       # execute_sql 工具（含写操作审批与安全校验）
│   ├── get_table_schema.go  # get_table_schema 工具
│   ├── get_sample_data.go   # get_sample_data 工具
│   ├── todo.go              # 任务规划（todo_write / todo_read）
│   └── memory.go            # 跨会话长记忆（memory_write / memory_read）
├── mcp/                     # MCP 客户端实现
│   ├── manager.go           # 多 server 管理、加载/重载配置
│   ├── client.go            # stdio / SSE 连接、握手、拉取工具
│   └── adapter.go           # 把 MCP 工具适配成内置 ToolDef
└── ui/
    └── model.go             # Bubble Tea 终端界面
```

## 环境要求

| 依赖 | 说明 |
|------|------|
| Go 1.26+ | 编译运行 |
| MySQL 5.7+ / 8.0+ | 被测数据库 |
| Node.js（含 `npx`） | 运行 npx 类 MCP server（fetch / filesystem / playwright） |
| Python 3.10+ | 运行 `time` MCP server（需 `pip install mcp-server-time`） |
| Google Chrome | `playwright` MCP server 自动启动浏览器时使用 |

> 如果只用内置数据库工具，只需 Go + MySQL；MCP 相关依赖可按需安装。

## 快速开始

### 1. 准备数据库

创建一个数据库（默认使用 `agentdb`），并确保里面有表可查：

```sql
CREATE DATABASE IF NOT EXISTS agentdb DEFAULT CHARSET utf8mb4;
```

### 2. 配置数据库连接

数据库连接串通过环境变量 `MYSQL_DSN` 提供，形如 `用户名:密码@tcp(localhost:3306)/数据库名`。启动前先设置：

```powershell
$env:MYSQL_DSN="用户名:密码@tcp(localhost:3306)/数据库名"
```

未设置该环境变量时，程序会提示并退出。账号密码不写入代码，避免误提交到仓库。

### 3. 配置 LLM

模型与接口地址在 [main.go](main.go#L29-L33) 中配置，默认使用 SiliconFlow 的 OpenAI 兼容接口 + `deepseek-ai/DeepSeek-V4-Flash`。

API Key 通过环境变量 `LLM_API_KEY` 读取（见 [main.go](main.go#L188-L190)），启动前先设置：

```powershell
$env:LLM_API_KEY="sk-你的key"
```

未设置该环境变量时，程序会给出提示并退出。

> 想换成别的服务商：把 `LLMBaseURL` 改成对应地址（如 OpenAI `https://api.openai.com/v1`、DeepSeek `https://api.deepseek.com/v1`），`LLMModel` 改成对应模型名即可。

### 4. 运行

在项目根目录（`mcp.json` 会被按相对路径读取）执行：

```powershell
go run .
```

启动后会出现 TUI 界面，直接输入问题、按 Enter 发送。

## 使用说明

### 对话

直接输入自然语言，例如：

```
帮我查一下 agentdb 里 users 表中 18-25 岁的用户有多少人
```

Agent 会先调用 `get_table_schema` 了解表结构，必要时调用 `get_sample_data` 抽样，最后用 `execute_sql` 查询。

### 写操作审批

写入类操作（`INSERT` / `UPDATE` / `DELETE` / `ALTER` / `CREATE` 等）不会立即执行：

1. Agent 首次提交写 SQL 时，工具会返回 **"待用户确认"**，并给出 SQL 和预计影响行数；
2. 你在对话中回复 **"确认 / 同意 / ok / yes / 执行"** 等表示同意的内容；
3. Agent 再次提交**完全相同**的 SQL，此时才会真正执行。

如果你回复了"不要 / 取消 / 算了"等否定内容，或输入了其它无关问题，挂起的写操作会自动作废。

### 终端快捷键

| 按键 | 作用 |
|------|------|
| `Enter` | 发送当前输入 |
| `Ctrl+C` / `q` | 退出程序 |
| 鼠标滚轮 | 上下滚动消息 |

### 上下文压缩

长对话每轮都要把全部历史重发，容易把上下文撑爆。程序会自动做两级压缩（见 [core/session.go](core/session.go)），只在上下文达到窗口的 80% 时触发，避免频繁压缩破坏 prompt 缓存：

1. **清理旧工具结果**：把较早的工具输出替换成占位符，保留 system 与最近 12 条消息，信息损失最小。
2. **摘要压缩（兜底）**：若清理后仍逼近上限，则调用模型把更早的对话压缩成一段摘要文本，替换掉原始历史。

窗口上限、触发比例、保留条数等阈值集中在 [core/session.go](core/session.go#L11-L18)，按模型窗口调整即可。

### 跨会话长记忆

当你说"记住……/以后都用……"时，Agent 会调用 `memory_write` 把内容写进运行目录下的 `agent_memory.json`；需要回忆时调用 `memory_read`。文件是普通 JSON（`{"键": "内容"}`），可以直接打开查看或手动编辑，重启后依然有效。

> `agent_memory.json` 已在 `.gitignore` 中忽略，不会随仓库上传。

### 任务规划

复杂任务（多步查询、迁移、批量修改）时，Agent 会先调用 `todo_write` 列出步骤，执行中逐步更新 `done` 状态。当前计划每轮都会临时附加到上下文（不写入历史，不会累积），因此长任务更不容易迷路。

> 计划本质是"写在上下文里的一段文字"，模型会参考但不会强制严格遵守——它提高的是执行条理，不是硬约束。

## MCP 工具扩展

MCP 让 Agent 能调用外部工具。配置写在 [mcp.json](mcp.json)。

### 配置格式

```json
{
  "servers": [
    {
      "name": "my-server",
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@some/mcp-server"],
      "env": ["KEY=value"]
    },
    {
      "name": "remote",
      "transport": "sse",
      "url": "http://localhost:8080/sse"
    }
  ]
}
```

| 字段 | 说明 |
|------|------|
| `name` | server 名称（工具名会以它为前缀） |
| `transport` | `stdio`（默认）或 `sse` |
| `command` / `args` | stdio 模式：启动命令及参数 |
| `env` | stdio 模式：追加环境变量，格式 `"KEY=value"` |
| `url` | sse 模式：远程服务地址 |

> MCP 工具在 Agent 中暴露的名字为 `<server名>_<工具名>`，例如 `filesystem_read_file`、`playwright_browser_navigate`，以避免与内置工具冲突。

### 内置示例

仓库自带的 `mcp.json` 配置了 4 个常用 server：

| server | 能力 |
|--------|------|
| `fetch` | 抓取网页内容 |
| `filesystem` | 读写指定目录下的文件（默认限定为项目目录） |
| `time` | 获取当前时间、时区换算 |
| `playwright` | 浏览器自动化：自动启动 Chrome，可导航、点击、填表、截图 |

### 运行时命令

在 TUI 输入框中可直接执行 `/mcp` 命令：

```
/mcp list                         查看已连接的 server 与工具
/mcp add <name> <command> [args...]   临时添加一个 stdio server
/mcp add <name> <http-url>            临时添加一个远程 SSE server
/mcp remove <name>                   断开某个 server
/mcp reload                          按 mcp.json 重新加载全部 server
/mcp help                            查看帮助
```

修改 `mcp.json` 后，执行 `/mcp reload` 即可生效，无需重启。注意首次通过 `npx` 拉取某个 server 时会现场下载依赖，可能稍慢。

## 内置工具

| 工具名 | 作用 | 参数 |
|--------|------|------|
| `get_table_schema` | 获取当前库所有表的结构（表名、字段、类型、主键、是否可空）。生成 SQL 前必须先调用 | 无 |
| `get_sample_data` | 查看指定表的前 5 行真实数据，了解字段实际取值样式 | `table_name` |
| `execute_sql` | 执行一条 SQL。`SELECT` 直接执行（自动补 `LIMIT 100`）；写操作需用户确认 | `sql` |
| `memory_write` | 把一条信息写入本地记忆文件（跨会话保留），相同 `key` 覆盖旧值 | `key`、`content` |
| `memory_read` | 读取记忆；不传 `key` 返回全部，传 `key` 返回该条 | `key`（可选） |
| `todo_write` | 创建/更新任务计划（整体替换），计划保存在会话中 | `todos`（数组，每项含 `step`、`done`） |
| `todo_read` | 读取当前计划与各步骤完成状态 | 无 |

## 安全机制

内置的 `execute_sql` 工具内置了多层保护（见 [tools/execute_sql.go](tools/execute_sql.go)）：

- **永远禁止**：`DROP`、`TRUNCATE`（含 `DROP TABLE` / `DROP DATABASE`）。
- **需二次确认**：`INSERT` / `UPDATE` / `DELETE` / `REPLACE` / `ALTER` / `CREATE` / `RENAME`。
- **强制 WHERE**：`DELETE` / `UPDATE` 必须带 `WHERE` 子句，禁止全表操作。
- **行数上限**：影响超过 **100 行**的写操作会被拒绝。
- **单语句限制**：一次只允许提交一条语句（中间不允许出现分号）。
- **防重复调用**：相同工具 + 相同参数连续调用 3 次会中止本轮，防止模型陷入死循环。

## 运行参数

| 参数 | 位置 | 默认值 | 说明 |
|------|------|--------|------|
| `MaxLoop` | [main.go](main.go#L21) | `40` | 单轮对话最多发起多少次 LLM / 工具循环 |
| 单次请求超时 | [main.go](main.go#L262) | `60s` | 每次 LLM 请求的超时 |
| 整轮超时 | [ui/model.go](ui/model.go#L47) | `5min` | 一轮完整问答的总超时 |
| 上下文窗口上限 `ContextWindowTokens` | [core/session.go](core/session.go#L11-L18) | `128000` | 估算 token 的窗口上限，按模型调整 |
| 压缩触发比例 `CompactTriggerRatio` | [core/session.go](core/session.go#L11-L18) | `0.8` | 达到窗口的该比例即触发压缩 |
| 压缩保留消息数 `KeepRecentMessages` | [core/session.go](core/session.go#L11-L18) | `12` | 压缩时保留最近的消息条数 |

## 常见问题

**Q：启动就 panic "数据库连接失败"？**
检查 MySQL 是否已启动、`agentdb` 是否存在、`MYSQL_DSN` 是否正确。

**Q：对话框报 "达到最大循环次数" 或 "context deadline exceeded"？**
通常是任务需要反复抓取网页（如查天气）导致链路过长或超时。建议改用能直接返回结构化数据的工具/接口，或适当调大 `MaxLoop` 与超时时间。

**Q：MCP server 连接失败？**
`npx` 类 server 首次需联网下载，确认 Node.js 已安装、网络可用；`time` server 需先 `pip install mcp-server-time`。

**Q：playwright 启动的 Chrome 顶部有"不受支持的命令行标记"提示条？**
这是 Chrome 对自动化启动参数（`--disable-blink-features=AutomationControlled`）的提醒，属正常现象，点关闭即可，不影响使用。
