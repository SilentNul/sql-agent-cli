package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

// 建立 MySQL 连接
func InitDB() (*sql.DB, error) {
	dsn := os.Getenv("MYSQL_DSN") //读取环境变量
	if dsn == "" {
		return nil, fmt.Errorf("未设置环境变量 MYSQL_DSN，请先设置，例如：$env:MYSQL_DSN=\"用户名:密码@tcp(localhost:3306)/数据库名\"")
	}
	db, err := sql.Open("mysql", dsn) //创建一个数据库连接池对象
	if err != nil {
		return nil, err
	}
	//验证连接是否可用
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// 执行一条查询 SQL，把结果集转成 JSON 字符串。
func queryToJSON(db *sql.DB, query string, args ...any) (string, error) { //query：查询语句，args：查询语句中的参数
	rows, err := db.Query(query, args...) //执行查询语句
	if err != nil {
		return "", err
	}
	defer rows.Close() //关闭查询结果集

	cols, err := rows.Columns() //获取查询结果集的列名
	if err != nil {
		return "", err
	}

	results := make([]map[string]any, 0)
	vals := make([]any, len(cols))
	for rows.Next() {
		// 每一列放一个 *any，Scan 会把值写进去（动态列的标准写法）
		ptrs := make([]any, len(cols))
		for i := range ptrs {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}

		row := make(map[string]any, len(cols))
		for i, name := range cols {
			v := vals[i]
			// MySQL 驱动把字符串类型返回成 []byte，转成 string 便于阅读
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[name] = v
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	b, err := json.Marshal(results)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// 清理 LLM 输出中可能带有的 markdown 代码块标记和多余空白。
func cleanSQL(raw string) string {
	s := strings.TrimSpace(raw)

	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSpace(s)
		// 跳过语言标记行（如 "sql"、"mysql"）
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			lang := strings.ToLower(strings.TrimSpace(s[:idx]))
			if lang == "sql" || lang == "mysql" {
				s = s[idx+1:]
			}
		}
	}

	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
