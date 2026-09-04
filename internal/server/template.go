package server

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"
)

var templates *template.Template
var templateDir string

// 模板函数
var templateFuncs = template.FuncMap{
	"sub": func(a, b int) int {
		return a - b
	},
	"add": func(a, b int) int {
		return a + b
	},
	"mul": func(a, b float64) float64 {
		return a * b
	},
	"div": func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"split": func(s, sep string) []string {
		if s == "" {
			return nil
		}
		var result []string
		start := 0
		for i := 0; i < len(s); i++ {
			if string(s[i]) == sep {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
		result = append(result, s[start:])
		return result
	},
	// reltime 相对时间显示（"4 分钟前"）
	"reltime": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "刚刚"
		case d < time.Hour:
			return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%d 小时前", int(d.Hours()))
		case d < 30*24*time.Hour:
			return fmt.Sprintf("%d 天前", int(d.Hours()/24))
		default:
			return t.Format("2006-01-02")
		}
	},
}

// InitTemplates 初始化模板
func InitTemplates(dir string) error {
	templateDir = dir
	var err error
	templates, err = template.New("").Funcs(templateFuncs).ParseGlob(filepath.Join(templateDir, "*.html"))
	return err
}

// renderTemplate 渲染模板
func renderTemplate(w http.ResponseWriter, name string, data PageData) {
	if templateDir == "" {
		// 模板未初始化，返回简单 HTML
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><h1>` + name + `</h1><p>模板未加载</p></body></html>`))
		return
	}

	// 为每个页面动态组合 layout + content 模板
	layoutPath := filepath.Join(templateDir, "layout.html")
	contentPath := filepath.Join(templateDir, name+".html")

	tmpl, err := template.New("").Funcs(templateFuncs).ParseFiles(layoutPath, contentPath)
	if err != nil {
		http.Error(w, "模板加载失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// 执行 layout 模板
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
