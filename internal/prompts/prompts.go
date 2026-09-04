package prompts

import (
	"bytes"
	"embed"
	"log"
	"os"
	"sync"
	"text/template"
)

//go:embed embedded/*.txt
var embeddedFS embed.FS

// Manager Prompt 模板管理器
type Manager struct {
	mu        sync.RWMutex
	templates map[string]*template.Template
	dir       string // 外部模板目录（可选）
}

// NewManager 创建模板管理器
func NewManager(dir string) *Manager {
	m := &Manager{
		templates: make(map[string]*template.Template),
		dir:       dir,
	}
	m.loadAll()
	return m
}

// loadAll 加载所有模板
func (m *Manager) loadAll() error {
	// 1. 先加载内嵌模板
	entries, err := embeddedFS.ReadDir("embedded")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := embeddedFS.ReadFile("embedded/" + name)
		if err != nil {
			continue
		}
		tmpl, err := template.New(name).Parse(string(data))
		if err != nil {
			log.Printf("解析内嵌模板 %s 失败: %v", name, err)
			continue
		}
		m.templates[name] = tmpl
	}

	// 2. 外部模板覆盖内嵌模板（如果存在）
	if m.dir != "" {
		if info, err := os.Stat(m.dir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(m.dir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					name := entry.Name()
					data, err := os.ReadFile(m.dir + "/" + name)
					if err != nil {
						continue
					}
					tmpl, err := template.New(name).Parse(string(data))
					if err != nil {
						log.Printf("解析外部模板 %s 失败: %v", name, err)
						continue
					}
					m.templates[name] = tmpl
					log.Printf("使用外部模板: %s", name)
				}
			}
		}
	}

	log.Printf("加载了 %d 个 Prompt 模板", len(m.templates))
	return nil
}

// Execute 执行指定模板
func (m *Manager) Execute(name string, data interface{}) (string, error) {
	m.mu.RLock()
	tmpl, ok := m.templates[name]
	m.mu.RUnlock()

	if !ok {
		return "", ErrTemplateNotFound
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Reload 重新加载所有模板
func (m *Manager) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates = make(map[string]*template.Template)
	m.loadAll()
}

// ErrTemplateNotFound 模板未找到错误
var ErrTemplateNotFound = &templateError{"template not found: "}

type templateError struct {
	msg string
}

func (e *templateError) Error() string {
	return e.msg
}
