package database

import "log"

// stripMarkdownEmphasisInDB 清除存量标题/摘要列中的 Markdown 加粗定界符（**）。
// 历史上 LLM 偶发在 summary/one_line_summary/话题标题与摘要中输出 **xxx**，
// 这些字段在界面按纯文本展示，星号会裸露。REPLACE 天然幂等，无 ** 的行不受影响。
// 仅处理纯文本展示列；正文/翻译列可能含代码中的 **（指针写法等合法场景），不处理。
func (d *DB) stripMarkdownEmphasisInDB() {
	cols := [][2]string{
		{"articles", "title"}, {"articles", "ai_summary"}, {"articles", "one_line_summary"},
		{"topics", "title"}, {"topics", "ai_summary"},
		{"reports", "summary"}, // 列表摘要按纯文本展示；content 保留 markdown 供预览渲染
	}
	for _, c := range cols {
		res, err := d.db.Exec(`UPDATE ` + c[0] + ` SET ` + c[1] + ` = REPLACE(` + c[1] + `, '**', '') WHERE ` + c[1] + ` LIKE '%**%'`)
		if err != nil {
			log.Printf("Markdown 清理: 更新 %s.%s 失败: %v", c[0], c[1], err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("Markdown 清理: %s.%s 已清理 %d 行", c[0], c[1], n)
		}
	}
}
