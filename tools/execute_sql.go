package tools

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
)

// 写操作允许的最大影响行数，超过则直接拒绝执行
const MaxAffectedRows = 100

// 永远禁止的操作
var forbiddenOpRe = regexp.MustCompile(`(?i)^\s*(DROP|TRUNCATE)\b`)

// 需要人工确认的写操作
var writeOpRe = regexp.MustCompile(`(?i)^\s*(INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|RENAME)\b`)

// 写操作审批状态
var (
	approvalMu   sync.Mutex
	pendingSQL   string // 等待用户确认的写操作
	pendingArmed bool   // 用户已确认，允许执行
)

// 在用户回复确认后调用，授权执行上一条挂起的写操作。
func ArmWriteApproval() {
	approvalMu.Lock()
	defer approvalMu.Unlock()
	if pendingSQL != "" {
		pendingArmed = true
	}
}

// 当新的一条用户消息不是确认时调用，作废挂起的写操作。
func CancelWriteApproval() {
	approvalMu.Lock()
	defer approvalMu.Unlock()
	pendingSQL = ""
	pendingArmed = false
}

// 用于比对：去掉结尾分号、压缩空白、转小写
func normalizeSQL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimSpace(s)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// 设定工具
func NewExecuteSQL(db *sql.DB) *ToolDef {
	return &ToolDef{
		Name:        "execute_sql",
		Description: "执行 SQL 语句。SELECT 直接执行。写操作（INSERT/UPDATE/DELETE/ALTER/CREATE 等）不会立即执行：本工具会返回“待用户确认”，你需要向用户说明将要执行的 SQL 并等待用户在对话中确认；用户确认后，再次提交完全相同的 SQL 即可真正执行。DROP 和 TRUNCATE 永远禁止。DELETE/UPDATE 必须包含 WHERE 子句。影响超过 100 行的写操作会被拒绝。SQL 必须基于 get_table_schema 返回的真实表和字段编写。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"sql": map[string]string{
					"type":        "string",
					"description": "要执行的 SQL 语句（一条语句，结尾可以带分号）",
				},
			},
			"required": []string{"sql"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			rawSQL, ok := args["sql"].(string)
			if !ok || strings.TrimSpace(rawSQL) == "" {
				return "", fmt.Errorf("参数 sql 必填且必须是非空字符串")
			}

			// 清理 markdown 标记 + 容忍模型习惯性加上的结尾分号
			query := strings.TrimSpace(cleanSQL(rawSQL))
			query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
			upper := strings.ToUpper(query)

			//中间仍存在分号代表一次提交了多条语句，拒绝
			if strings.Contains(query, ";") {
				return "", fmt.Errorf("不允许一次提交多条语句（分号只能出现在结尾）")
			}

			//DROP/TRUNCATE永远禁止
			if forbiddenOpRe.MatchString(query) {
				op := strings.TrimSpace(forbiddenOpRe.FindString(query))
				return "", fmt.Errorf("%q 操作过于危险，永远禁止执行", op)
			}

			//SELECT：直接执行
			if strings.HasPrefix(upper, "SELECT") {
				if !strings.Contains(upper, "LIMIT") {
					query += " LIMIT 100" //加上限制
				}
				return queryToJSON(db, query)
			}

			//写操作
			if !writeOpRe.MatchString(query) {
				return "", fmt.Errorf("不支持的 SQL 类型，只允许 SELECT 和写操作（INSERT/UPDATE/DELETE/ALTER 等）")
			}

			//DELETE/UPDATE 必须有 WHERE 子句，禁止全表操作
			if strings.HasPrefix(upper, "DELETE") || strings.HasPrefix(upper, "UPDATE") {
				if !strings.Contains(upper, "WHERE") {
					return "", fmt.Errorf("DELETE/UPDATE 必须包含 WHERE 子句，禁止全表操作")
				}
			}

			//只有"已授权 + 与挂起 SQL 完全相同"才会被执行
			approvalMu.Lock()
			approved := pendingSQL != "" && pendingArmed &&
				normalizeSQL(pendingSQL) == normalizeSQL(query)
			//将授权返回初始状态
			if approved {
				pendingSQL = ""
				pendingArmed = false
			}
			approvalMu.Unlock()

			if !approved {
				// 首次提交：估算影响行数，登记为挂起，等待用户确认
				affected, err := estimateAffectedRows(db, query)
				if err != nil {
					return "", fmt.Errorf("预估影响行数失败: %v", err)
				}
				if affected >= 0 && affected > MaxAffectedRows {
					return "", fmt.Errorf("该操作将影响 %d 行，超过最大允许行数 %d，拒绝执行", affected, MaxAffectedRows)
				}
				approvalMu.Lock()
				pendingSQL = query
				pendingArmed = false
				approvalMu.Unlock()

				countDesc := "未知"
				if affected >= 0 {
					countDesc = fmt.Sprintf("%d", affected)
				}
				return fmt.Sprintf(
					"待用户确认，尚未执行。\nSQL: %s\n预计影响行数: %s\n请向用户说明该 SQL 并等待用户确认；用户确认后，再次提交完全相同的 SQL 即可执行。",
					query, countDesc,
				), nil
			}

			// 已授权，执行前查看影响行数
			affected, err := estimateAffectedRows(db, query)
			if err != nil {
				return "", fmt.Errorf("预估影响行数失败: %v", err)
			}
			if affected >= 0 && affected > MaxAffectedRows {
				return "", fmt.Errorf("该操作将影响 %d 行，超过最大允许行数 %d，拒绝执行", affected, MaxAffectedRows)
			}

			result, err := db.Exec(query)
			if err != nil {
				return "", fmt.Errorf("执行失败: %v", err)
			}
			rowsAffected, _ := result.RowsAffected()
			return fmt.Sprintf("执行成功，影响 %d 行", rowsAffected), nil
		},
	}
}

// 估算写操作会影响多少行。
func estimateAffectedRows(db *sql.DB, sqlText string) (int64, error) {
	upper := strings.ToUpper(sqlText)

	//SELECT COUNT(*) FROM table WHERE ...
	if strings.HasPrefix(upper, "DELETE") {
		countSQL := regexp.MustCompile(`(?i)^DELETE\b`).ReplaceAllString(sqlText, "SELECT COUNT(*)")
		var count int64
		if err := db.QueryRow(countSQL).Scan(&count); err != nil {
			return 0, err
		}
		return count, nil
	}

	// SELECT COUNT(*) FROM table WHERE ...
	if strings.HasPrefix(upper, "UPDATE") {
		re := regexp.MustCompile(`(?is)^UPDATE\s+(\S+)\s+SET\s+.*?\s+WHERE\s+(.*)$`)
		m := re.FindStringSubmatch(sqlText)
		if len(m) == 3 {
			countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", m[1], m[2])
			var count int64
			if err := db.QueryRow(countSQL).Scan(&count); err != nil {
				return 0, err
			}
			return count, nil
		}
		return -1, nil
	}

	// INSERT/ALTER/CREATE/REPLACE等无法简单估算
	return -1, nil
}
