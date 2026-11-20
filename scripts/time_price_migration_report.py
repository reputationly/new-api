#!/usr/bin/env python3
"""时段价改造：现网影响面报告（只读，本地跑）。

输入：从生产库导出的 options 快照（JSON 对象："选项名" -> "选项值字符串"），
     或一个目录，内含 选项名.json 文件。
输出：Markdown 报告（命中矩阵 / override 明细 / 摊平冲突 / 其他阻塞项）。

用法：
    python3 scripts/time_price_migration_report.py options.json > report.md
    python3 scripts/time_price_migration_report.py ./options_dir > report.md

只读、不联网、不写文件。解析语义与 setting/ratio_setting 一致：
    pickRuleFrom: 精确(权重2) > 尾部通配(权重1)，同权重取模式串更长者
    Layer4(现状): 命中时段 -> 终值 = Base x 时段值（取代模型定价）
    Layer4(改造后): 终值 = 常规价 x 时段系数（叠加）
"""
import json
import os
import sys

TIME_FUNCS = ("hour(", "minute(", "weekday(", "month(", "day(")


def load_options(path):
    if os.path.isdir(path):
        out = {}
        for name in os.listdir(path):
            if name.endswith(".json"):
                with open(os.path.join(path, name), encoding="utf-8") as f:
                    out[name[:-5]] = f.read()
        return out
    with open(path, encoding="utf-8") as f:
        raw = json.load(f)
    return {k: (v if isinstance(v, str) else json.dumps(v)) for k, v in raw.items()}


def jload(opts, key, default):
    s = opts.get(key)
    if s is None or not s.strip():
        return default
    try:
        return json.loads(s)
    except Exception:
        return default


def match_weight(pattern, model):
    if pattern == model:
        return 2
    if pattern.endswith("*") and model.startswith(pattern[:-1]):
        return 1
    return 0


def pick(rules, model):
    best_w, best_p, best_v = 0, None, None
    for pat, val in (rules or {}).items():
        w = match_weight(pat, model)
        if w == 0:
            continue
        if w > best_w or (w == best_w and best_p is not None and len(pat) > len(best_p)):
            best_w, best_p, best_v = w, pat, val
    return best_p, best_v


def norm_rule(v):
    if isinstance(v, (int, float)):
        return "multiply", float(v)
    if isinstance(v, dict):
        return (v.get("mode") or "multiply"), float(v.get("value") or 0)
    return "multiply", 0.0


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)

    opts = load_options(sys.argv[1])
    model_ratio = jload(opts, "ModelRatio", {})
    model_price = jload(opts, "ModelPrice", {})
    group_ratio = jload(opts, "GroupRatio", {})
    group_group = jload(opts, "GroupGroupRatio", {})
    group_model = jload(opts, "GroupModelRatio", {})
    time_cfg = jload(opts, "GroupTimeRatio", {})
    billing_mode = jload(opts, "ModelBillingMode", {})
    billing_expr = jload(opts, "ModelBillingExpr", {})
    disabled = set(jload(opts, "GroupEnabled", []) or [])

    windows = (time_cfg or {}).get("windows") or {}
    time_rules = (time_cfg or {}).get("rules") or {}
    models = sorted(set(model_ratio) | set(model_price) | set(billing_mode))

    identity_by_line = {}
    for u, m in (group_group or {}).items():
        if isinstance(m, dict):
            for line in m:
                identity_by_line.setdefault(line, set()).add(u)

    rows = []
    bucket = {"none": 0, "multiply": 0, "override": 0}
    tiered_time = []

    for model in models:
        bm = (billing_mode or {}).get(model, "")
        expr = (billing_expr or {}).get(model, "") or ""
        if bm == "tiered_expr" and any(f in expr for f in TIME_FUNCS):
            tiered_time.append(model)

        for line, pat_map in (time_rules or {}).items():
            if not isinstance(pat_map, dict):
                continue
            pat, rules = pick(pat_map, model)
            if pat is None:
                continue
            base = float((group_ratio or {}).get(line, 1) or 0)
            gpat, graw = pick((group_model or {}).get(line) or {}, model)
            if gpat is None:
                mode, gval = "none", 1.0
            else:
                mode, gval = norm_rule(graw)
            normal = gval if mode == "override" else base * gval
            bucket[mode] = bucket.get(mode, 0) + 1
            for r in rules or []:
                tv = float(r.get("value") or 0)
                win = r.get("window") or ""
                old = base * tv
                rows.append({
                    "line": line, "model": model, "tpat": pat, "mode": mode,
                    "base": base, "normal": normal, "window": win, "tv": tv,
                    "old": old, "keep_tf": (old / normal) if normal else 0.0,
                    "fix": normal * tv,
                    "identity": sorted(identity_by_line.get(line, [])),
                    "disabled": line in disabled,
                })

    def money(name, ratio):
        if name in model_price and name not in model_ratio:
            return "CNY %.4f/call" % (float(model_price[name]) * ratio)
        return "%.4fx" % ratio

    print("# 时段价改造 · 现网影响面报告\n")
    print("> 只读统计，未修改任何配置。倍率均以「相对目录价 1.0」表示；按次计费模型换算成元/次。\n")

    print("## 0. 快照概况\n")
    print("| 项目 | 数量 |")
    print("|---|---|")
    print("| 目录价模型（ModelRatio） | %d |" % len(model_ratio))
    print("| 按次计费模型（ModelPrice） | %d |" % len(model_price))
    print("| 线路（GroupRatio） | %d |" % len(group_ratio))
    print("| 时段模板 | %d |" % len(windows))
    print("| 配了时段规则的线路 | %d |" % len([g for g, v in (time_rules or {}).items() if v]))
    print("| 时段规则条目（线路×模式串×模板） | %d |" % len(rows))
    print()

    print("## 1. 命中矩阵（同一条时段规则身上叠了什么）\n")
    print("| 组合 | 条数 | 换算 |")
    print("|---|---|---|")
    print("| 时段 + 无模型定价 | %d | 不变（tf = 原值） |" % bucket.get("none", 0))
    print("| 时段 + 折扣 ×（multiply） | %d | 自动：tf = 时段值 ÷ 折扣 |" % bucket.get("multiply", 0))
    print("| 时段 + 定价 =（override） | %d | 价格会变，需选口径 |" % bucket.get("override", 0))
    print("| tiered_expr 且表达式含时间函数 | %d | 双重时间轴，需处理 |" % len(tiered_time))
    print()
    if tiered_time:
        print("表达式里用了 hour/weekday 等时间函数的 tiered 模型（这些模型不能再配时段价）：\n")
        for m in tiered_time:
            print("- " + m)
        print()

    ov = [r for r in rows if r["mode"] == "override"]
    print("## 2. override × 时段 明细（决定「保价」还是「纠正」）\n")
    if not ov:
        print("**没有命中**：现网不存在「定价 =」与时段规则同时生效的组合，迁移可全自动、无涨跌风险。\n")
    else:
        print("| 线路 | 模型 | 时段 | 时段值 | 现终值 | 常规价 | 保价系数 | 纠正后 | 纠正涨跌 |")
        print("|---|---|---|---|---|---|---|---|---|")
        for r in sorted(ov, key=lambda x: (x["line"], x["model"], x["window"]))[:200]:
            delta = (r["fix"] / r["old"] - 1) * 100 if r["old"] else 0
            warn = " ⚠身份覆盖" if r["identity"] else ""
            print("| %s | %s | %s | %s | %s | %s | %.4f | %s | %+.1f%%%s |" % (
                r["line"], r["model"], r["window"], r["tv"], money(r["model"], r["old"]),
                money(r["model"], r["normal"]), r["keep_tf"], money(r["model"], r["fix"]),
                delta, warn))
        if len(ov) > 200:
            print("\n（仅列前 200 行，共 %d 行）" % len(ov))
        print()
        print("- 保价系数：填这个值，该时段价格与现在逐位相同。纠正后：按「锁价打折」重算的价格。")
        print("- 带身份覆盖标记的行，保价系数随用户档变化，无法用一个值保价。\n")

    print("## 3. 摊平冲突（v1 只有全局层，按线路的值必须合并）\n")
    agg = {}
    for r in rows:
        agg.setdefault((r["tpat"], r["window"]), []).append((r["line"], r["keep_tf"]))
    print("| 模式串 | 模板 | 各线路换算后系数 | 状态 |")
    print("|---|---|---|---|")
    for (pat, win), items in sorted(agg.items()):
        vals = {round(tf, 9) for _, tf in items}
        desc = "、".join("%s=%.4f" % (l, tf) for l, tf in sorted(items))
        print("| %s | %s | %s | %s |" % (pat, win, desc, "❌ 冲突需人工选" if len(vals) > 1 else "✅ 可合并"))
    conflicts = sum(1 for items in agg.values() if len({round(tf, 9) for _, tf in items}) > 1)
    print()
    print("冲突数：**%d**（0 = 可全自动摊平）\n" % conflicts)

    print("## 4. 其他阻塞项\n")
    cov = [r for r in rows if r["identity"]]
    if cov:
        print("- 存在身份覆盖（GroupGroupRatio）的线路，其 Base 随用户档变化，override 组合无法用一个系数保价：")
        for l in sorted({r["line"] for r in cov}):
            print("  - %s <- %s" % (l, "、".join(sorted(identity_by_line[l]))))
    else:
        print("- 没有身份覆盖：所有组合都能精确自动换算（保价可全自动）。")
    dis = sorted({r["line"] for r in rows if r["disabled"]})
    print("- 停用线路上的时段规则：%s" % ("、".join(dis) if dis else "无"))

    stale = []
    for line, pat_map in (time_rules or {}).items():
        for pat in (pat_map or {}):
            if pat == "*":
                continue
            if not any(match_weight(pat, m) for m in models):
                stale.append(line + " / " + pat)
    print("- 模式串匹配不到任何模型的废规则：%s" % ("、".join(sorted(stale)) if stale else "无"))
    print()


if __name__ == "__main__":
    main()
