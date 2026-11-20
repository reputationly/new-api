
"""Deterministic structural checks for H3 video prompts (official vs ours).

Only mechanical facts: section presence, shot timing legality, asset-label usage,
verbatim dialogue, CJK leakage, hedge words. Semantic judgement stays with a human.
"""
import json, re, sys, glob, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vcases

BASE = ["integrated_multimodal_description", "overall_soundscape", "non_diegetic_music"]
REF = ["subject_definitions", "summary", "retention_analysis", "detailed_description",
       "overall_soundscape", "non_diegetic_music"]

def norm(s):
    return re.sub(r"[^a-z0-9]+", " ", s.lower()).strip()

def sections(p):
    n = norm(p)
    want = REF if True else BASE
    return n

def has(p, name):
    return " " + norm(name) + " " in " " + norm(p) + " "

CUT = re.compile(r"At\s+(\d{1,2}):(\d{2})\.(\d{2,3})")
DIA = re.compile(r"<d>\[([A-Za-z ]+)\]\s*(.*?)</d>", re.S)

def shot_times(p):
    ts = []
    for m in re.finditer(r"\[Shot\s+(\d+)\]", p):
        pass
    # pair each cut marker with the shot it opens
    for m in CUT.finditer(p):
        mm, ss, frac = m.group(1), m.group(2), m.group(3)
        t = int(mm) * 60 + int(ss) + int(frac) / (10 ** len(frac))
        ts.append(t)
    return ts

def shots(p):
    nums = [int(x) for x in re.findall(r"\[Shot\s+(\d+)\]", p)]
    return sorted(set(nums)) if nums else []

def analyze(cid, case, prompt):
    r = {"id": cid, "task": case["task"], "chars": len(prompt)}
    want = REF if "r2v" in case["task"] else BASE
    r["missing_sections"] = [s for s in want if not has(prompt, s)]
    r["shots"] = shots(prompt)
    r["cuts"] = [round(t, 3) for t in shot_times(prompt)]
    r["dur"] = case["dur"]
    # labels
    lab = {}
    for kind in ("Picture", "Video", "Audio", "Subject"):
        nums = sorted({int(x) for x in re.findall(r"<" + kind + r"\s+(\d+)>", prompt)})
        lab[kind] = nums
    media = {}
    for k, _, _ in case["media"]:
        media[k] = media.get(k, 0) + 1
    r["labels"] = lab
    r["media"] = media
    kind2media = {"Picture": "image", "Video": "video", "Audio": "audio"}
    avail = {k: media.get(kind2media.get(k, ""), 0) for k in ("Picture", "Video", "Audio")}
    # 参考视频自带的音轨会被官方编成 <Audio 1>，用户再给的音频顺延，所以 r2v+video 时音频可用数 +1
    if "video" in media:
        avail["Audio"] += 1
    r["avail"] = avail
    r["bad_labels"] = {k: [n for n in v if n > avail[k]] for k, v in lab.items() if k in avail and any(n > avail[k] for n in v)}
    # dialogue
    dial = [m.group(2).strip() for m in DIA.finditer(prompt)]
    r["dialogue"] = dial
    # user-requested verbatim Chinese lines
    want_lines = re.findall(r"[\u201c\u2018\"]([^\u201d\u2019\"]{2,40})[\u201d\u2019\"]", case["prompt"])
    r["requested_lines"] = want_lines
    r["lines_kept"] = [("Y" if l in prompt else "N") + ":" + l for l in want_lines]
    # CJK outside <d>..</d> and outside quotes
    stripped = DIA.sub(" ", prompt)
    # 引号里的内容是画面文字（招牌、标语），官方也直接写成中文，不算泄漏
    stripped = re.sub(r'"[^"]*"', ' ', stripped)
    stripped = re.sub(r'\u201c[^\u201d]*\u201d', ' ', stripped)
    cjk = [ch for ch in stripped if "\u4e00" <= ch <= "\u9fff"]
    r["cjk_outside_dialogue"] = "".join(cjk)[:80]
    r["hedges"] = len(re.findall(r"\bor\b", prompt, re.I))
    r["preamble_frame"] = bool(re.match(r"^(For the target video|How the reference pictures)", prompt.strip()))
    if "r2v" in case["task"]:
        marks = ["fully_preserved", "partially_preserved", "attribute_transfer", "partially_copy", "reference"]
        r["retention_marks"] = sorted({m for m in marks if m in prompt})
        r["summary_tag"] = bool(re.search(r"^\s*\[[a-z +]+\]", prompt, re.M))
    return r

def load(dirpath, name):
    p = os.path.join(dirpath, name + ".json")
    if not os.path.exists(p):
        hits = sorted(glob.glob(os.path.join(dirpath, name + "_*.json")))
        if not hits:
            return None
        p = hits[0]
    d = json.load(open(p))
    if "task" in d and isinstance(d["task"], dict):
        return (d["task"].get("content") or {}).get("prompt") or ""
    if "prompt" in d:
        return d["prompt"]
    if "h3_prompt" in d:
        return d["h3_prompt"]
    if "rounds" in d:
        return d.get("h3_prompt") or ""
    return None

if __name__ == "__main__":
    dirs = sys.argv[1:]
    cases = {c["id"]: c for c in vcases.CASES}
    for d in dirs:
        print("=" * 70)
        print("DIR", d)
        for cid, case in cases.items():
            p = load(d, cid)
            if p is None:
                print(f"{cid:14s} [no output]")
                continue
            r = analyze(cid, case, p)
            bits = [f"{cid:14s} {r['chars']:5d}c shots={r['shots']} cuts={r['cuts']}/{r['dur']}s"]
            if r["missing_sections"]:
                bits.append("MISSING=" + ",".join(r["missing_sections"]))
            bl = {k: v for k, v in r["bad_labels"].items() if v}
            if bl:
                bits.append("BADLABEL=" + json.dumps(bl))
            if any(x.startswith("N") for x in r["lines_kept"]):
                bits.append("DIALOGUE_LOST=" + json.dumps([x for x in r["lines_kept"] if x.startswith("N")], ensure_ascii=False))
            if r["cjk_outside_dialogue"]:
                bits.append("CJK_OUT=" + r["cjk_outside_dialogue"])
            bits.append(f"or={r['hedges']}")
            if "r2v" in case["task"]:
                bits.append("marks=" + ",".join(r.get("retention_marks") or []) + (" tag" if r.get("summary_tag") else " NOTAG"))
            else:
                bits.append("preamble=" + ("Y" if r["preamble_frame"] else "n"))
            print(" | ".join(bits))
