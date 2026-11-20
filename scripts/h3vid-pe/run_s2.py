
"""候选配置：在 run_sc.py 的逐字复刻基础上，加可控开关（max_tokens / 有效时长 / 附加规则）。

用法: python3 run_s2.py <none|low> <outdir> [额外规则文件|-] [ids...]
环境变量:
  MAXTOK=8192   显式 max_tokens（0 = 不发，等价现网）
  EFF=1         证据里带 effective_duration_seconds（帧栅格对齐后的交付时长）
  TAG=xxx       输出文件名后缀
"""
import json, os, sys, time, concurrent.futures as cf
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE); sys.path.insert(0, os.path.join(HERE, "..", "..", ".h3exp", "h3pe"))
import vcases, lib
D = os.path.join(HERE, "..", "..", "service", "h3v20") + os.sep
rd = lambda p: open(D + p, encoding="utf-8").read()
# 顺序与 service/aggregate_enhance_singlecall.go 的 buildSingleCallSystem 逐字一致：
# skill → base-en → [ref-en，仅 r2v] → shot-planning → rules → appendix → writing+证据。
# appendix.txt 是我们的合规清单（唯一非逐字移植的一段），生产里确实会发。
SKILL, BASE, REF, SHOT, RULES, APPENDIX, WRITING = (
    rd("skills/prompt-writing.SKILL.md"), rd("skills/base-en.txt"), rd("skills/ref-en.txt"),
    rd("skills/shot-planning.SKILL.md"), rd("rules.txt"), rd("appendix.txt"), rd("writing.txt"))

cfg, out = sys.argv[1], sys.argv[2]; os.makedirs(out, exist_ok=True)
EXTRA = open(sys.argv[3]).read() if len(sys.argv) > 3 and sys.argv[3] != "-" else ""
ids = sys.argv[4:] or [c["id"] for c in vcases.CASES]
MODEL = "qwen3.8-flash-fp8"
MAXTOK = int(os.environ.get("MAXTOK", "0") or 0)
EFF = os.environ.get("EFF", "") not in ("", "0")
TAG = os.environ.get("TAG", "")
# NORF=1 不发 response_format —— 生产已经不发（qwen3.8 + json_object 会被 xgrammar 拒成 500）
NORF = os.environ.get("NORF", "") not in ("", "0")
# NOVIDEO=1 参考视频不发给模型（生产 plan C），证据里只留 <Video N> 标签 + observable:false
NOVIDEO = os.environ.get("NOVIDEO", "") not in ("", "0")

def eff_seconds(sec):
    n = max(1, round(sec * 24))
    while n % 17 != 5:
        n += 1
    return round(n / 24, 3)

def system(c):
    assets, cnt = [], {}
    names = {"image": "Picture", "video": "Video", "audio": "Audio"}
    role_map = {"first_frame": "first_frame", "last_frame": "last_frame"}
    has_video = any(k == "video" for k, _, _ in c["media"])
    for kind, src, role in c["media"]:
        cnt[kind] = cnt.get(kind, 0) + 1
        a = {"asset_id": f"{kind}_{cnt[kind]}", "media_type": kind,
             "role": role_map.get(role, "reference"), "official_label": f"<{names[kind]} {cnt[kind]}>",
             "observable": (kind == "image")}
        # 与 Go 侧一致:有参考视频时音频不写死编号(引擎会给视频音轨先占一个槽)
        if kind == "audio" and has_video:
            a["official_label"] = ""
            a["label_unknown"] = True
        assets.append(a)
    task = {"type": c["task"], "duration_seconds": float(c["dur"]), "generate_audio": True}
    if EFF:
        task["effective_duration_seconds"] = eff_seconds(c["dur"])
    ev = {"user_request": c["prompt"], "task": task, "assets": assets}
    parts = [SKILL, BASE] + ([REF] if "r2v" in c["task"] else []) + [SHOT, RULES, APPENDIX]
    if EXTRA:  # 实验用附加规则，排在清单之后（生产不发这一段）
        parts.append(EXTRA)
    parts.append(WRITING + json.dumps(ev, ensure_ascii=False, separators=(",", ":")))
    return "\n\n".join(parts)

def user(c):
    parts = [{"type": "text", "text": c["prompt"]}]
    for kind, src, role in c["media"]:
        if kind == "image": parts.append({"type": "image_url", "image_url": {"url": vcases.src_url(src)}})
        if kind == "video":
            if NOVIDEO:
                continue
            parts.append({"type": "video_url", "video_url": {"url": src}})
    return parts if len(parts) > 1 else c["prompt"]

def kw():
    k = {}
    if not NORF:
        k["response_format"] = {"type": "json_object"}
    if MAXTOK:
        k["max_tokens"] = MAXTOK
    if cfg == "none":
        k["chat_template_kwargs"] = {"enable_thinking": False}
    else:
        k["reasoning_effort"] = cfg
    return k

def one(c):
    msgs = [{"role": "system", "content": system(c)}, {"role": "user", "content": user(c)}]
    t0 = time.time(); rec = {"cfg": cfg, "maxtok": MAXTOK, "eff": EFF, "rounds": []}
    for rnd in range(2):
        st, r, _el = lib.chat(MODEL, msgs, **kw())
        if st != 200 or not isinstance(r, dict) or "choices" not in r:
            rec["rounds"].append({"status": st, "error": str(r)[:500]}); break
        ch = r["choices"][0]; m = ch["message"]; raw = m.get("content") or ""
        rec["rounds"].append({"raw": raw, "usage": r.get("usage"), "finish": ch.get("finish_reason")})
        try:
            j = json.loads(raw[raw.find("{"): raw.rfind("}") + 1])
            rec["h3_prompt"] = j.get("h3_prompt", ""); rec["plan"] = j.get("content_plan")
            rec["uncertainties"] = j.get("uncertainties")
            break
        except Exception:
            msgs += [{"role": "assistant", "content": raw},
                     {"role": "user", "content": 'Repair ONLY these transport errors, keeping semantic content unchanged:\n["Response must be a JSON object"]'}]
    rec["elapsed"] = round(time.time() - t0, 1)
    name = c["id"] + (("_" + TAG) if TAG else "")
    json.dump(rec, open(f"{out}/{name}.json", "w"), ensure_ascii=False, indent=1)
    return c["id"], rec["elapsed"], len(rec.get("h3_prompt") or ""), rec["rounds"][-1].get("finish")

with cf.ThreadPoolExecutor(4) as ex:
    for res in ex.map(one, [c for c in vcases.CASES if c["id"] in ids]): print(*res, flush=True)
