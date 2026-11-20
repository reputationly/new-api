"""复刻 new-api singlecall（service/aggregate_enhance_singlecall.go）的请求，跑 MaaS 模型。"""
import json, os, sys, time, re, concurrent.futures as cf
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE); sys.path.insert(0, os.path.join(HERE, "..", "..", ".h3exp", "h3pe"))
import vcases, lib
D = os.path.join(HERE, "..", "..", "service", "h3v20") + os.sep
rd = lambda p: open(D + p, encoding="utf-8").read()
SKILL, BASE, REF, SHOT, RULES, APPENDIX, WRITING = (
    rd("skills/prompt-writing.SKILL.md"), rd("skills/base-en.txt"), rd("skills/ref-en.txt"),
    rd("skills/shot-planning.SKILL.md"), rd("rules.txt"), rd("appendix.txt"), rd("writing.txt"))
cfg, out = sys.argv[1], sys.argv[2]; os.makedirs(out, exist_ok=True)
EXTRA = open(sys.argv[3]).read() if len(sys.argv) > 3 and sys.argv[3] != "-" else ""
ids = sys.argv[4:] or [c["id"] for c in vcases.CASES]
MODEL = "qwen3.8-flash-fp8"
def system(c):
    assets, cnt = [], {}
    names = {"image": "Picture", "video": "Video", "audio": "Audio"}
    role_map = {"first_frame": "first_frame", "last_frame": "last_frame"}
    for kind, src, role in c["media"]:
        if kind == "audio":  # new-api 不把音频编进 CompilerInput
            continue
        cnt[kind] = cnt.get(kind, 0) + 1
        assets.append({"asset_id": f"{kind}_{cnt[kind]}", "media_type": kind,
                       "role": role_map.get(role, "reference"), "official_label": f"<{names[kind]} {cnt[kind]}>"})
    ev = {"user_request": c["prompt"], "task": {"type": c["task"], "duration_seconds": float(c["dur"]), "generate_audio": True}, "assets": assets}
    parts = [SKILL, BASE] + ([REF] if "r2v" in c["task"] else []) + [SHOT, RULES, APPENDIX]
    if EXTRA: parts.append(EXTRA)
    parts.append(WRITING + json.dumps(ev, ensure_ascii=False, separators=(",", ":")))
    return "\n\n".join(parts)
def user(c):
    parts = [{"type": "text", "text": c["prompt"]}]
    for kind, src, role in c["media"]:
        if kind == "image": parts.append({"type": "image_url", "image_url": {"url": vcases.src_url(src)}})
        if kind == "video": parts.append({"type": "video_url", "video_url": {"url": src}})
    return parts if len(parts) > 1 else c["prompt"]
def kw():
    k = {"response_format": {"type": "json_object"}}
    if cfg == "none": k["chat_template_kwargs"] = {"enable_thinking": False}
    else: k["reasoning_effort"] = cfg
    return k
def one(c):
    msgs = [{"role": "system", "content": system(c)}, {"role": "user", "content": user(c)}]
    t0 = time.time(); rec = {"cfg": cfg, "rounds": []}
    for rnd in range(2):
        try:
            # lib.chat 返回 (status, body, elapsed)，不是 dict。
            st, r, _el = lib.chat(MODEL, msgs, **kw())
        except Exception as e:
            rec["rounds"].append({"error": str(e)[:500]}); break
        if st != 200 or not isinstance(r, dict) or "choices" not in r:
            rec["rounds"].append({"status": st, "error": str(r)[:500]}); break
        m = r["choices"][0]["message"]; raw = m.get("content") or ""
        rec["rounds"].append({"raw": raw, "usage": r.get("usage")})
        try:
            j = json.loads(raw[raw.find("{"): raw.rfind("}") + 1]); rec["h3_prompt"] = j.get("h3_prompt", ""); rec["plan"] = j.get("content_plan"); break
        except Exception:
            msgs += [{"role": "assistant", "content": raw}, {"role": "user", "content": 'Repair ONLY these transport errors, keeping semantic content unchanged:\n["Response must be a JSON object"]'}]
    rec["elapsed"] = round(time.time() - t0, 1)
    json.dump(rec, open(f"{out}/{c['id']}.json", "w"), ensure_ascii=False, indent=1)
    return c["id"], rec["elapsed"], len(rec.get("h3_prompt") or ""), (rec["rounds"][-1].get("error") or "")[:150]
with cf.ThreadPoolExecutor(8) as ex:
    for res in ex.map(one, [c for c in vcases.CASES if c["id"] in ids]): print(*res, flush=True)
