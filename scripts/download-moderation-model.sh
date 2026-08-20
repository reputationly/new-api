#!/usr/bin/env bash
#
# 下载内容审核模型权重。见 docs/content-moderation-design.md §5。
#
# 用法：
#   ./download-moderation-model.sh                    # 默认下 guard8b，走 ModelScope
#   ./download-moderation-model.sh guard4b            # 下 4B（用于 §15 第 3 条的 8B/4B 对比）
#   SOURCE=hf ./download-moderation-model.sh          # 改走 HuggingFace
#   TARGET_DIR=/mnt/models ./download-moderation-model.sh
#   ./download-moderation-model.sh guard8b vl8b       # 一次下多个
#
# 环境变量：
#   SOURCE      modelscope（默认）| hf
#   TARGET_DIR  权重根目录，默认 /data/models
#   HF_ENDPOINT HF 镜像，默认 https://hf-mirror.com（SOURCE=hf 时生效）
#   HF_TOKEN    HF 访问令牌（Qwen 系列公开，通常不需要）
#
set -euo pipefail

SOURCE="${SOURCE:-modelscope}"
TARGET_DIR="${TARGET_DIR:-/data/models}"
HF_ENDPOINT="${HF_ENDPOINT:-https://hf-mirror.com}"

MODEL_KEYS="guard8b guard4b guard0.6b vl8b"

# 用 case 而非关联数组：macOS 自带 bash 3.2 没有 declare -A，
# 脚本要能在开发机上先试跑一遍再挂到管理节点。
repo_of() {
  case "$1" in
    guard8b)   echo "Qwen/Qwen3Guard-Gen-8B" ;;
    guard4b)   echo "Qwen/Qwen3Guard-Gen-4B" ;;
    guard0.6b) echo "Qwen/Qwen3Guard-Gen-0.6B" ;;
    vl8b)      echo "Qwen/Qwen3-VL-8B-Instruct" ;;
    *)         echo "" ;;
  esac
}
size_of() {
  case "$1" in
    guard8b)   echo "~17 GB" ;;
    guard4b)   echo "~9 GB" ;;
    guard0.6b) echo "~1.5 GB" ;;
    vl8b)      echo "~18 GB" ;;
  esac
}
purpose_of() {
  case "$1" in
    guard8b)   echo "第一期 L1 文本判定（主力）" ;;
    guard4b)   echo "备选，用于与 8B 做自有样本集对比（§15 第 3 条）" ;;
    guard0.6b) echo "仅供本地冒烟测试，不要上生产" ;;
    vl8b)      echo "第二期 L2 图片判定，准召不达标则不上线" ;;
  esac
}
# 每个 key 的磁盘预留（GB，含解压与临时文件余量）
need_of() {
  case "$1" in
    guard8b|vl8b) echo 20 ;;
    guard4b)      echo 11 ;;
    guard0.6b)    echo 3 ;;
    *)            echo 0 ;;
  esac
}

die() { echo "错误: $*" >&2; exit 1; }

usage() {
  local mk
  echo "用法: $0 [model-key ...]"
  echo
  printf '  %-12s %-28s %-10s %s\n' KEY REPO 大小 用途
  for mk in $MODEL_KEYS; do
    printf '  %-12s %-28s %-10s %s\n' "$mk" "$(repo_of "$mk")" "$(size_of "$mk")" "$(purpose_of "$mk")"
  done
  echo
  echo "环境变量: SOURCE=modelscope|hf  TARGET_DIR=/data/models  HF_ENDPOINT=https://hf-mirror.com"
}

# Qwen3Guard-Gen 需要 transformers>=4.51.0；vLLM 侧另有要求，见 §5.1。
check_cli() {
  case "$SOURCE" in
    modelscope)
      command -v modelscope >/dev/null 2>&1 || \
        die "未找到 modelscope CLI，先执行: pip install -U modelscope"
      ;;
    hf)
      if command -v hf >/dev/null 2>&1; then
        HF_BIN=hf
      elif command -v huggingface-cli >/dev/null 2>&1; then
        HF_BIN=huggingface-cli
      else
        die "未找到 hf CLI，先执行: pip install -U 'huggingface_hub[cli]'"
      fi
      ;;
    *)
      die "SOURCE 只能是 modelscope 或 hf，当前: $SOURCE"
      ;;
  esac
}

# 下载前检查剩余空间。8B bf16 权重 17 GB，下到一半磁盘满会留下一堆残片，
# 而断点续传对残缺的 safetensors 分片并不总能自愈——提前拦住比事后清理省事。
check_disk() {
  local need_gb=$1
  local avail_kb avail_gb
  # df -Pk 是 POSIX 的，Linux 与 macOS 都支持；-BG --output 是 GNU 专有，不能用。
  avail_kb=$(df -Pk "$TARGET_DIR" 2>/dev/null | tail -1 | awk '{print $4}')
  if ! echo "$avail_kb" | grep -qE '^[0-9]+$'; then
    echo "提示: 无法读取 $TARGET_DIR 的剩余空间，跳过检查（需约 ${need_gb}GB）" >&2
    return 0
  fi
  avail_gb=$((avail_kb / 1024 / 1024))
  if [ "$avail_gb" -lt "$need_gb" ]; then
    die "$TARGET_DIR 剩余 ${avail_gb}GB，不足所需 ${need_gb}GB"
  fi
}

download_one() {
  local key=$1
  local repo
  repo="$(repo_of "$key")"
  [ -n "$repo" ] || { usage; die "未知的 model-key: $key"; }

  local dest="$TARGET_DIR/${repo##*/}"
  mkdir -p "$dest"

  echo "==> $key  $repo  ($(size_of "$key"))  →  $dest"

  case "$SOURCE" in
    modelscope)
      # ModelScope CLI 自带断点续传，重复执行会跳过已完成分片。
      modelscope download --model "$repo" --local_dir "$dest"
      ;;
    hf)
      # --resume-download 在新版 hf CLI 中已是默认行为，显式传会告警，故不传。
      HF_ENDPOINT="$HF_ENDPOINT" "$HF_BIN" download "$repo" --local-dir "$dest"
      ;;
  esac

  verify_one "$dest"
}

# 校验：权重目录至少要有 config.json 与一个 safetensors 分片。
# 只看目录非空是不够的——中断的下载会留下 .incomplete 缓存，目录看着有东西，
# vLLM 起来才报错，那时已经排查到部署层去了。
verify_one() {
  local dest=$1
  [ -f "$dest/config.json" ] || die "$dest 缺少 config.json，下载未完成"
  if ! ls "$dest"/*.safetensors >/dev/null 2>&1; then
    die "$dest 没有 safetensors 权重分片，下载未完成"
  fi
  if find "$dest" -name '*.incomplete' -print -quit | grep -q .; then
    die "$dest 存在 .incomplete 残片，重跑本脚本续传"
  fi
  local total
  total=$(du -sh "$dest" 2>/dev/null | cut -f1)
  echo "    校验通过，实际占用 $total"
}

main() {
  case "${1:-}" in
    -h|--help|help) usage; exit 0 ;;
  esac

  local keys="$*"
  [ -n "$keys" ] || keys="guard8b"

  check_cli
  mkdir -p "$TARGET_DIR"

  local need=0
  for k in $keys; do
    [ -n "$(repo_of "$k")" ] || { usage; die "未知的 model-key: $k"; }
    need=$((need + $(need_of "$k")))
  done
  check_disk "$need"

  echo "源: $SOURCE    目标: $TARGET_DIR"
  for k in $keys; do
    download_one "$k"
  done

  echo
  echo "全部完成。接入 GPUStack："
  echo "  1) 添加 Model Deployment，模型源选「本地路径」指向 $TARGET_DIR/<模型目录>"
  echo "  2) backend 选 vLLM，参数 --max-model-len 8192（起步值，见设计文档 §6.3 第八条）"
  echo "  3) 拿到 OpenAI 兼容端点与 API key，填进 new-api「系统设置 → 内容审核」"
}

main "$@"
