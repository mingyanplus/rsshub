package database

import "log"

// deleteOrphanRows 清理子表中的存量孤儿行。
// 历史缺陷：外键 PRAGMA 曾只对单个池化连接生效，依赖 ON DELETE CASCADE 的删除
// （清空文章、删订阅源等）在部分连接上未级联，子表残留指向已删父行的记录。
// 父表为空时 NOT IN (空集) 恒真，恰好把子表清空——与"清空所有文章"的语义一致。
// 幂等：无孤儿时各语句删 0 行；子表外键列均有索引，启动时执行一次开销可忽略。
func (d *DB) deleteOrphanRows() {
	orphans := [][2]string{
		{"article_tags", "article_id"}, {"article_tags", "tag_id"},
		{"report_articles", "article_id"}, {"report_articles", "report_id"},
		{"follow_rule_notifications", "article_id"}, {"follow_rule_notifications", "rule_id"},
		{"event_articles", "article_id"}, {"event_articles", "event_id"},
		{"topic_articles", "article_id"}, {"topic_articles", "topic_id"},
		{"exposures", "article_id"},
		{"read_logs", "article_id"},
	}
	for _, o := range orphans {
		parent := map[string]string{
			"article_id": "articles", "tag_id": "tags", "report_id": "reports",
			"rule_id": "follow_rules", "event_id": "event_tracks", "topic_id": "topics",
		}[o[1]]
		res, err := d.db.Exec("DELETE FROM "+o[0]+" WHERE "+o[1]+" NOT IN (SELECT id FROM "+parent+")")
		if err != nil {
			log.Printf("孤儿行清理: %s.%s 失败: %v", o[0], o[1], err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("孤儿行清理: %s.%s 已删除 %d 行", o[0], o[1], n)
		}
	}

	// notifications.report_id 无外键约束（表早于外键规范），按逻辑关系补一刀
	res, err := d.db.Exec(`DELETE FROM notifications WHERE report_id IS NOT NULL AND report_id NOT IN (SELECT id FROM reports)`)
	if err != nil {
		log.Printf("孤儿行清理: notifications 失败: %v", err)
	} else if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("孤儿行清理: notifications 已删除 %d 行", n)
	}
}
