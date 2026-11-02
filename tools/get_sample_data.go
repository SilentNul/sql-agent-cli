package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
)

// 抽样看数据
func NewGetSampleData(db *sql.DB) *ToolDef {
	return &ToolDef{
		Name:        "get_sample_data",
		Description: "查看指定表的前 5 行真实数据，用于了解字段的实际取值样式（例如 gender 字段存的是 '男' 还是 'M'）。在编写带过滤条件的 SQL 前，建议先抽样了解数据",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"table_name": map[string]string{
					"type":        "string",
					"description": "要查看的表名，必须是 get_table_schema 返回结果中真实存在的表名",
				},
			},
			"required": []string{"table_name"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			tableName, ok := args["table_name"].(string)
			tableName = strings.TrimSpace(tableName)
			if !ok || tableName == "" {
				return "", fmt.Errorf("参数 table_name 必填且必须是非空字符串")
			}

			// 查看表名真实存在
			var cnt int
			err := db.QueryRow(
				`SELECT COUNT(*) FROM information_schema.tables
				 WHERE table_schema = DATABASE() AND table_name = ?`,
				tableName).Scan(&cnt)
			if err != nil {
				return "", err
			}
			if cnt == 0 {
				return "", fmt.Errorf("表 %q 不存在于当前数据库，请先调用 get_table_schema 获取真实表名", tableName)
			}

			return queryToJSON(db, fmt.Sprintf("SELECT * FROM `%s` LIMIT 5", tableName))
		},
	}
}
