# AI Reader

自托管的 AI 资讯聚合阅读器：抓取 RSS / 网页 / JSON API 订阅，用本地 LLM 逐篇分析，自动把同一事件的多源报道聚合为话题流，生成早晚报与日报。

报纸编辑风界面 —— 报绿 `#1a6b3c` × 赤陶 `#b87848`，宋体标题，单色 SVG 图标。

## 功能

**订阅源**

- **RSS / Atom / JSON Feed**：标准源直接订阅
- **HTML 网页**：任意列表页用 CSS 选择器抓取（无 RSS 的站点也能订）；选择器支持 `选择器@属性` 语法（如 `img@alt` 取图片标题、`@data-url` 取条目自身属性）
- **编码识别**：GB2312 / GBK / Big5 等老站自动识别转码，识别不准可手动指定
- **JSON API**：gojq 路径提取，支持从 curl 命令一键导入（含 headers/请求体）

**阅读**

- **话题流**：同一事件的多篇报道自动聚合为一个话题（向量 + 实体双门控），多源话题由 LLM 生成 150-250 字综合摘要；单来源话题直接使用文章 AI 摘要
- **24 小时热榜**：话题热度 = 多源交叉验证为主信号（来源数 × 1.5 + 文章数 × 0.5 + 平均重要度 × 0.3 + 来源权威度 × 0.6）
- **文章列表**：瀑布流 + 原文预览 + 中文翻译 + 一键获取原文（readability 正文提取）
- **历史时间线**：话题详情页展示同一主体的历史话题演进
- **访客模式**：双密码体系——管理员密码全权限，阅读密码登录的访客只能浏览（文章/话题/报告/分类），不能管理订阅、规则、设置与数据

**AI 分析**（每篇文章）

摘要 / 一句话总结 / 关键词 / 标签 / 实体 / 分类 / 重要度评分（1-10）/ 非中文自动翻译；提示词内置正文清洗规则（剔除频道引流、签名、来源标记等噪音）；订阅级内容过滤（每行一条正则或纯文本，抓取入库时逐行剔除，节省 token）。

**追踪与通知**

- **事件追踪**：自定义关键词（正向/负面）+ 角色标签 + 锚点向量持续追踪事件进展
- **早晚报 / 日报**：定时生成，李自然日报式排版（来源可点回原文验证）
- **多通道推送**：Gotify / 邮件 / QQ Bot / Webhook，每个通道都有连接测试
- **关注规则**：关键词订阅匹配 + 相似度推送提醒

**可靠性**

- 文章分析失败自动重试（3 次上限），支持单篇手动重试与一键批量重试
- LLM 响应缓存（同内容不重复调用）、API 限流、503 冷却退避
- **数据库备份**：SQLite 一致性快照（`VACUUM INTO`），支持手动备份/下载/删除，可配置定时自动备份与保留上限（超出自动清理最旧的）

**话题频道**

话题按频道分类展示（科技 / AI / 财经 / 国际 / 社会 / 生活 / 健康 / 教育 / 其他），分类关键词可在 `config.yaml` 的 `topics.categories` 自定义（按序匹配首个命中）。

**网络**

- 代理设置：`http://` / `socks5://`，内容抓取与 LLM 接口两通道独立开关，保存热生效
- 兼容 LM Studio 等本地服务（embeddings 错误格式差异已兼容）

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.25，chi 路由，html/template（运行时解析） |
| 数据库 | SQLite（modernc 纯 Go 驱动，免 CGO，单文件） |
| AI | 任意 OpenAI 兼容 API：LM Studio / Ollama / 云端均可，LLM 与 Embedding 可分开配置 |
| 前端 | Tailwind（CDN 运行时）+ htmx + 原生 JS，无构建步骤 |

## 快速开始

### Docker（推荐）

```bash
docker run -d \
  --name rss-ai \
  -p 8080:8080 \
  -v rss-ai-data:/data \
  ghcr.io/mingyanplus/rsshub:latest
```

首次启动自动生成 `/data/config.yaml`，编辑后重启容器生效。

数据库与配置均存放在 `/data` 卷内，升级（拉新镜像后删除重建容器）不会丢数据：

```bash
docker pull ghcr.io/mingyanplus/rsshub:latest
docker rm -f rss-ai
docker run -d --name rss-ai -p 8080:8080 -v rss-ai-data:/data ghcr.io/mingyanplus/rsshub:latest
```

> v1.0.1 及更早版本存在已知问题：数据库实际写在容器内 `/app/data/rss.db`（未在卷内），容器删除重建后文章数据会丢失。v1.0.2 起启动时自动迁移到 `/data/rss.db`。若旧容器尚未删除，可先 `docker cp rss-ai:/app/data/rss.db ./rss.db.bak` 抢救数据，再复制进卷内恢复。

### 二进制

从 [Releases](https://github.com/mingyanplus/rsshub/releases) 下载对应平台压缩包（含二进制 + 模板 + 示例配置），解压后：

```bash
cp config.example.yaml config.yaml   # 编辑 LLM/Embedding 配置
./rss-ai -config config.yaml
```

### 源码

```bash
go build -o rss-ai ./cmd/server
./rss-ai
```

打开 http://localhost:8080 → 设置页填入 LLM / Embedding API 地址与密钥 → 点「测试连接」验证 → 添加订阅源。

## 配置

`config.yaml` 关键段（完整见 `config.example.yaml`）：

```yaml
ai:
  llm:
    base_url: "http://localhost:1234/v1"   # LM Studio / Ollama / 云端
    model: "qwen3.5-4b"
    api_key: "..."
  embedding:
    base_url: "http://localhost:1234/v1"
    model: "text-embedding-bge-large-zh-v1.5"

scheduler:
  refresh_interval: 30    # RSS 抓取间隔（分钟）

server:
  password: "..."         # 管理员密码
  reader_password: "..."  # 阅读密码（访客只读，留空不启用）

data_backup:              # 数据库自动备份
  auto_enable: true
  interval: 24h           # 距最近一次备份超过该时长才触发
  max_files: 7            # 保留上限，超出删最旧

proxy:                    # 可选：两通道独立开关
  url: "http://127.0.0.1:7890"
  enable_content: true    # 内容抓取走代理
  enable_llm: false       # LLM 接口走代理
```

所有配置均可在 Web 设置页修改并热生效（含推送、代理、定时任务）。

> 注意：更换 Embedding 模型后，新旧向量空间不兼容，建议批量重算文章向量并重建话题。

## 发版

推送 `v*` 标签自动触发 CI：

```bash
./scripts/release.sh          # 版本号自动递增（v1.0.0 → v1.0.1）
./scripts/release.sh v1.2.0   # 或指定版本
```

流水线自动完成（[Actions](https://github.com/mingyanplus/rsshub/actions)）：

- 编译 linux/amd64、linux/arm64、windows/amd64 三平台二进制，挂到 GitHub Release
- 构建多架构 Docker 镜像推送 GHCR（版本号 + `latest` 双标签）

## 调度节奏

| 任务 | 频率 |
|---|---|
| 订阅抓取 | 每 30 分钟（可配） |
| AI 分析待处理文章 | 每 10 分钟，篇间限速 |
| 话题聚合 | 文章分析完成的瞬间（事件驱动） |
| 热点事件检测 | 每 6 小时 |
| 数据库自动备份 | 每 5 分钟检查，到期间隔才执行 |

## License

MIT
