package database

import (
	"database/sql"
	"log"
	"time"
)

// sqlTimeFormat 统一时间列的存储格式：UTC 文本（秒与偏移间无空格）。
// modernc/mattn 两驱动均可解析；文本字典序 = 时间序，SQL 侧排序/窗口过滤可靠。
// 历史上直接绑定 time.Time，不同驱动/时区写入的格式混杂
// （如 '2026-09-07 04:31:46 +0000 UTC'、'2026-09-07 16:20:08 +0800 CST m=+35...'），
// 文本比较会失真（最新排序错乱、24h 热榜窗口过滤失真、今日统计不准）。
const sqlTimeFormat = "2006-01-02 15:04:05.999999999+00:00"

// sqlTime 时间 → 统一存储文本（绑定 SQL 参数用；UTC 墙上值保证同基准可比较）
func sqlTime(t time.Time) string {
	return t.UTC().Format(sqlTimeFormat)
}

// normalizeTimeColumns 把历史混杂格式的时间列统一为规范 UTC 文本。
// 预筛规则：不以 '+00:00' 结尾的值视为需迁移（规范格式与 mattn UTC 格式均以
// '+00:00' 结尾且排序基准同为 UTC 墙上值，无需改动）；其余读出后经驱动解析
// 转换写回。幂等：已规范的行不命中预筛。失败仅记日志，不阻断启动。
func (d *DB) normalizeTimeColumns() {
	cols := [][2]string{
		{"articles", "published_at"}, {"articles", "fetched_at"},
		{"feeds", "created_at"}, {"feeds", "last_fetched_at"},
		{"topics", "first_article_at"}, {"topics", "last_updated_at"},
		{"topics", "created_at"}, {"topics", "summary_updated_at"},
		{"reports", "created_at"}, {"reports", "sent_at"},
	}
	for _, c := range cols {
		rows, err := d.db.Query(
			`SELECT id, ` + c[1] + ` FROM ` + c[0] + ` WHERE ` + c[1] + ` IS NOT NULL AND ` + c[1] + ` NOT LIKE '%+00:00'`)
		if err != nil {
			log.Printf("时间列规范化: 查询 %s.%s 失败: %v", c[0], c[1], err)
			continue
		}
		type fix struct {
			id   int64
			norm string
		}
		var fixes []fix
		for rows.Next() {
			var id int64
			var t sql.NullTime
			if err := rows.Scan(&id, &t); err != nil || !t.Valid {
				continue
			}
			fixes = append(fixes, fix{id, sqlTime(t.Time)})
		}
		rows.Close()
		for _, f := range fixes {
			if _, err := d.db.Exec(`UPDATE `+c[0]+` SET `+c[1]+` = ? WHERE id = ?`, f.norm, f.id); err != nil {
				log.Printf("时间列规范化: 更新 %s.%s id=%d 失败: %v", c[0], c[1], f.id, err)
			}
		}
		if len(fixes) > 0 {
			log.Printf("时间列规范化: %s.%s 已统一 %d 行", c[0], c[1], len(fixes))
		}
	}
}
