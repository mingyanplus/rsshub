#!/bin/sh
set -e

# 兼容任意挂载方式（命名卷 / bind mount）：修正 /data 属主为 app 用户
chown -R app:app /data 2>/dev/null || true

# 首次启动生成默认配置（镜像内示例已指向 /data、监听 0.0.0.0）；
# 旧版本卷中的相对路径数据库与 127.0.0.1 监听地址迁移（幂等，仅匹配默认值，不影响自定义配置）
if [ ! -f /data/config.yaml ]; then
    cp /app/config.example.yaml /data/config.yaml
fi
sed -i -e 's#path: "./data/rss.db"#path: "/data/rss.db"#' -e 's#host: "127.0.0.1"#host: "0.0.0.0"#' /data/config.yaml

# 降权为主程序用户运行
exec su-exec app:app /app/rss-ai -config /data/config.yaml
