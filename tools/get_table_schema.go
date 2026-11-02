package tools

import (
	"context"
	"database/sql"

	"github.com/openai/openai-go/v3"
)

// 获取表结构
func NewGetTableSchema(db *sql.DB) *ToolDef {
	return &ToolDef{
		Name:        "get_table_schema",
		Description: "获取当前数据库中所有表的结构信息，包括每张表的表名、字段名、字段类型、是否主键。在生成任何 SQL 查询之前，必须先调用本工具了解表结构",
		Params: openai.FunctionParameters{
			"type":       "object",
			"properties": map[string]any{
				// 不需要参数，但properties必须存在
			},
			"required": []string{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			return queryToJSON(db, `
				SELECT table_name, column_name, data_type, column_key, is_nullable
				FROM information_schema.columns
				WHERE table_schema = DATABASE()
				ORDER BY table_name, ordinal_position`)
		},
	}
}
