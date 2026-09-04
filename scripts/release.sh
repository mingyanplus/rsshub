#!/usr/bin/env bash
# 一键发版：校验工作区干净 → 提交推送 → 打版本标签 → 推送标签触发 CI
# CI 自动完成：多平台二进制（挂到 GitHub Release）+ Docker 镜像（推到 ghcr.io）
#
# 用法:
#   ./scripts/release.sh v1.0.0        # 指定版本号
#   ./scripts/release.sh               # 自动递增 patch（v1.2.3 -> v1.2.4）
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# ── 前置检查 ──────────────────────────────────────────────
if [ -n "$(git status --porcelain)" ]; then
  echo "✗ 工作区有未提交的改动，请先提交再发版："
  git status --short
  exit 1
fi

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
echo "→ 当前分支: ${BRANCH}"

# ── 确定版本号 ────────────────────────────────────────────
LAST_TAG="$(git tag --list 'v*' --sort=-v:refname | head -1 || true)"
if [ -z "${1:-}" ]; then
  if [ -z "${LAST_TAG}" ]; then
    VERSION="v1.0.0"
  else
    # 递增 patch 段
    VERSION="$(echo "${LAST_TAG}" | awk -F. '{ printf "v%d.%d.%d", $1, $2, $3+1 }' | sed 's/v/v/')"
  fi
else
  VERSION="${1}"
fi
case "${VERSION}" in
  v*) ;;
  *) VERSION="v${VERSION}" ;;
esac

if git rev-parse "${VERSION}" >/dev/null 2>&1; then
  echo "✗ 标签 ${VERSION} 已存在"
  exit 1
fi

echo "→ 上个版本: ${LAST_TAG:-无}，本次发版: ${VERSION}"
read -rp "确认发版 ${VERSION}? [y/N] " ok
[ "${ok}" = "y" ] || { echo "已取消"; exit 0; }

# ── 推送代码与标签 ────────────────────────────────────────
git push origin "${BRANCH}"
git tag -a "${VERSION}" -m "Release ${VERSION}"
git push origin "${VERSION}"

cat <<EOF

✓ 已推送标签 ${VERSION}，GitHub Actions 将自动执行：
  1. 编译 linux/amd64、linux/arm64、windows/amd64 二进制 → 挂到 Release
  2. 构建多架构 Docker 镜像 → ghcr.io/${IMAGE:-mingyanplus/rsshub}:${VERSION}

进度: https://github.com/mingyanplus/rsshub/actions
EOF
