#!/usr/bin/env bash
# 视频能力的上线冒烟：能力目录 → 1 s 生成 → 续写。
#
# 用法（全部走环境变量，不要把 key 写进脚本）：
#
#   BASE_URL=https://<你的域名> API_KEY=sk-xxx ./scripts/smoke-video-api.sh
#
# 可选：
#   SOURCE_URL=<一段 2–15 s 的 mp4 公网地址>   不提供则**跳过**续写用例
#   SKIP_GENERATE=1                           只验能力目录（不烧 GPU）
#   POLL_TIMEOUT=600                          单个任务的最长等待秒数
#
# 断言的是**契约**而不是"能跑"：目录里有 modes/deliveryDurations、交付时长按 17n+5
# 吸附（1 s → 1.625 s）、续写返回 concat_done 与三个时长。任何一条不满足就非零退出。

set -euo pipefail

BASE_URL="${BASE_URL:?需要 BASE_URL，例如 https://maas.example.com}"
API_KEY="${API_KEY:?需要 API_KEY（sk-...）}"
SOURCE_URL="${SOURCE_URL:-}"
POLL_TIMEOUT="${POLL_TIMEOUT:-600}"
SKIP_GENERATE="${SKIP_GENERATE:-0}"

if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
	echo "需要 curl 与 python3" >&2
	exit 1
fi

BASE_URL="${BASE_URL%/}"
AUTH=(-H "Authorization: Bearer ${API_KEY}")

fail() { echo "[FAIL] $*" >&2; exit 1; }
ok() { echo "[ ok ] $*"; }

# ── 1. 能力目录 ────────────────────────────────────────────────────────────
echo "== 1/3 能力目录"
CAPS="$(curl -sS -m 30 "${AUTH[@]}" "${BASE_URL}/v1/ov/capabilities")" || fail "拉能力目录失败"

CAPS="$CAPS" python3 - <<'PY' || exit 1
import json, os, sys
data = json.loads(os.environ["CAPS"])
videos = [c for c in data.get("capabilities", []) if c.get("operation") == "generate_video"]
if not videos:
    print("[FAIL] 目录里没有 generate_video 能力")
    sys.exit(1)
with_modes = [c for c in videos if c.get("modes")]
if not with_modes:
    print("[FAIL] 视频能力都没有 modes —— M4 的能力字段没上线，或目录里的模型没声明 image_mode")
    sys.exit(1)
cap = with_modes[0]
ids = [m["id"] for m in cap["modes"]]
print(f"[ ok ] {cap['model']} modes={ids}")
for need in ("video-extension", "first-last-frame", "reference"):
    if need not in ids:
        print(f"[warn] 缺少玩法 {need}（目录里该模型的 image_mode 选项不全）")
ext = next((m for m in cap["modes"] if m["id"] == "video-extension"), None)
if ext and not ext.get("requiresSourceVideo"):
    print("[FAIL] video-extension 应标 requiresSourceVideo")
    sys.exit(1)
durations = cap.get("deliveryDurations") or []
if durations:
    if abs(durations[0] - 1.625) > 1e-6:
        print(f"[FAIL] deliveryDurations[0]={durations[0]}，1 s 档应为 1.625")
        sys.exit(1)
    print(f"[ ok ] deliveryDurations 首档 {durations[0]}，共 {len(durations)} 档")
else:
    print("[warn] 没有 deliveryDurations（该模型不是 H3 引擎族，或引擎族未声明）")
PY
ok "能力目录检查通过"

if [ "${SKIP_GENERATE}" = "1" ]; then
	echo "== 跳过生成（SKIP_GENERATE=1）"
	exit 0
fi

# ── 2. 1 s 文生视频 ────────────────────────────────────────────────────────
echo "== 2/3 文生视频（1 s，验证 17n+5 吸附）"
MODEL="$(CAPS="$CAPS" python3 -c '
import json,os
d=json.loads(os.environ["CAPS"])
v=[c for c in d["capabilities"] if c.get("operation")=="generate_video" and c.get("modes")]
print(v[0]["model"] if v else "")
')"
[ -n "$MODEL" ] || fail "目录里没有带 modes 的视频模型"

SUBMIT="$(curl -sS -m 60 "${AUTH[@]}" -H 'content-type: application/json' \
	-d "{\"model\":\"${MODEL}\",\"prompt\":\"a calm blue sky with slow moving clouds\",\"duration\":1,\"metadata\":{\"image_mode\":\"text-to-video\",\"resolution\":\"768P\",\"generate_audio\":false}}" \
	"${BASE_URL}/v1/videos")"
TASK="$(SUBMIT="$SUBMIT" python3 -c 'import json,os;d=json.loads(os.environ["SUBMIT"]);print(d.get("id") or d.get("task_id") or "")')"
[ -n "$TASK" ] || fail "提交失败：${SUBMIT}"
ok "task=${TASK}"

deadline=$(( $(date +%s) + POLL_TIMEOUT ))
STATUS=""
while [ "$(date +%s)" -lt "$deadline" ]; do
	sleep 5
	Q="$(curl -sS -m 30 "${AUTH[@]}" "${BASE_URL}/v1/videos/${TASK}")"
	STATUS="$(Q="$Q" python3 -c 'import json,os;print(json.loads(os.environ["Q"]).get("status",""))')"
	case "$STATUS" in
		completed|succeeded|success) break ;;
		failed|failure|cancelled) fail "任务失败：${Q}" ;;
	esac
done
[ "$STATUS" = "completed" ] || [ "$STATUS" = "succeeded" ] || [ "$STATUS" = "success" ] || fail "轮询超时（${POLL_TIMEOUT}s），最后状态 ${STATUS}"

Q="$Q" python3 - <<'PY' || exit 1
import json, os, sys
d = json.loads(os.environ["Q"])
meta = d.get("metadata") or {}
dur = meta.get("duration_seconds")
frames = meta.get("frames")
if dur is None:
    print("[FAIL] 响应缺 metadata.duration_seconds —— 真实时长回显没上线")
    sys.exit(1)
if not (1.5 <= float(dur) <= 1.75):
    print(f"[FAIL] 1 s 请求的交付时长 {dur}，应为 1.625（±帧）")
    sys.exit(1)
print(f"[ ok ] duration_seconds={dur} frames={frames}")
PY
ok "生成用例通过"

# ── 3. 续写 ────────────────────────────────────────────────────────────────
if [ -z "${SOURCE_URL}" ]; then
	echo "== 3/3 跳过续写（未提供 SOURCE_URL）"
	exit 0
fi
echo "== 3/3 续写（源片 + 生成段 → 整段）"
SUBMIT="$(curl -sS -m 120 "${AUTH[@]}" -H 'content-type: application/json' \
	-d "{\"model\":\"${MODEL}\",\"prompt\":\"the clouds keep drifting right\",\"duration\":10,\"metadata\":{\"image_mode\":\"video-extension\",\"source_video\":\"${SOURCE_URL}\",\"resolution\":\"768P\",\"generate_audio\":false}}" \
	"${BASE_URL}/v1/videos")"
TASK="$(SUBMIT="$SUBMIT" python3 -c 'import json,os;d=json.loads(os.environ["SUBMIT"]);print(d.get("id") or d.get("task_id") or "")')"
[ -n "$TASK" ] || fail "续写提交失败：${SUBMIT}"
ok "task=${TASK}"

deadline=$(( $(date +%s) + POLL_TIMEOUT ))
while [ "$(date +%s)" -lt "$deadline" ]; do
	sleep 5
	Q="$(curl -sS -m 30 "${AUTH[@]}" "${BASE_URL}/v1/videos/${TASK}")"
	STATUS="$(Q="$Q" python3 -c 'import json,os;print(json.loads(os.environ["Q"]).get("status",""))')"
	case "$STATUS" in
		completed|succeeded|success) break ;;
		failed|failure|cancelled) fail "续写任务失败：${Q}" ;;
	esac
done
[ "$STATUS" = "completed" ] || [ "$STATUS" = "succeeded" ] || [ "$STATUS" = "success" ] || fail "续写轮询超时"

Q="$Q" python3 - <<'PY' || exit 1
import json, os, sys
d = json.loads(os.environ["Q"])
cont = (d.get("metadata") or {}).get("continuation")
if not cont:
    print("[FAIL] 响应缺 metadata.continuation —— 续写快照没落库/没回显")
    sys.exit(1)
if not cont.get("concat_done"):
    print(f"[FAIL] concat_done=false（{cont.get('concat_error') or '无原因'}）—— 拼接段没跑成功，交付的是生成段")
    sys.exit(1)
total = (d.get("metadata") or {}).get("duration_seconds")
print(f"[ ok ] source={cont.get('source_seconds')}s gen={cont.get('generate_seconds')}s total={total}")
PY
ok "续写用例通过"
echo
echo "全部通过。"
