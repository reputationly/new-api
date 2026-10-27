#!/usr/bin/env python3
"""前缀缓存命中率探测脚本（可独立分发，无内部依赖）。

原理
----
OpenAI 兼容接口的提示词缓存是「前缀匹配」：同一段前缀再次出现时，服务端会命中
缓存，并在响应的 usage.prompt_tokens_details.cached_tokens 里报告命中的 token 数。

本脚本构造一段随机长前缀（内容每次运行都不同，保证首次是冷请求），然后按顺序发
几次请求，观察 cached_tokens 的变化：

    1. 冷请求           —— 内容从未出现过，cached 应为 0
    2. 同 body 重发     —— 完全相同的请求，cached 应接近 prompt_tokens
    3. 换尾部           —— 前缀不变、只换末尾问题，cached 应仍然接近前缀长度
    4. 再等一段时间重发 —— 验证缓存是否持久

判定标准
--------
- 命中率 = cached_tokens / prompt_tokens
- 正常实现：第 2 次起命中率应 ≥ 90%
- 若始终为 0，说明该链路没有前缀缓存，或未开通 / 未透出统计

用法
----
    export UPSTREAM_API_KEY=sk-xxx
    python3 cache_probe.py --base-url https://api.vendor.com --model glm-5.3

    # 需要指定输出预算字段（推理模型常见）
    python3 cache_probe.py --base-url ... --model ... --max-tokens-field max_completion_tokens

    # 加大前缀、拉长间隔
    python3 cache_probe.py --base-url ... --model ... --prefix-tokens 16000 --interval 20

依赖：requests
"""
from __future__ import annotations

import argparse
import json
import os
import random
import sys
import time

import requests

# 一段与业务无关的中文词表，用于生成随机填充文本
WORDS = (
    "架构 推理 缓存 带宽 调度 集群 延迟 吞吐 显存 张量 量化 蒸馏 采样 对齐 "
    "检索 向量 训练 评估 基线 增量 熔断 降级 灰度 回滚 观测 埋点 分片 副本"
).split()


def gen_prefix(chars: int, seed: int) -> str:
    """生成唯一随机文本。每次运行 seed 不同，确保第一次是真正的冷请求。"""
    rnd = random.Random(seed)
    out: list[str] = []
    n = 0
    while n < chars:
        s = " ".join(rnd.choice(WORDS) for _ in range(12)) + f" 段{rnd.randint(10**6, 10**7 - 1)}。"
        out.append(s)
        n += len(s)
    return "".join(out)[:chars]


def call(base: str, headers: dict, model: str, content: str,
         field: str, cap: int, timeout: int) -> dict:
    payload = {"model": model, "messages": [{"role": "user", "content": content}],
               "temperature": 0, field: cap}
    t0 = time.perf_counter()
    try:
        r = requests.post(f"{base}/chat/completions", headers=headers,
                          json=payload, timeout=timeout)
    except Exception as e:                      # noqa: BLE001 —— 探测脚本要容错
        return {"ok": False, "err": f"{type(e).__name__}: {e}",
                "latency": time.perf_counter() - t0}
    dt = time.perf_counter() - t0
    try:
        body = r.json()
    except ValueError:
        return {"ok": False, "err": f"http={r.status_code} 非 JSON: {r.text[:120]}",
                "latency": dt}
    if r.status_code != 200:
        return {"ok": False, "err": f"http={r.status_code} {json.dumps(body, ensure_ascii=False)[:160]}",
                "latency": dt}

    usage = body.get("usage") or {}
    details = usage.get("prompt_tokens_details") or {}
    prompt = usage.get("prompt_tokens") or 0
    cached = details.get("cached_tokens")
    return {
        "ok": True,
        "prompt": prompt,
        "cached": cached if cached is not None else 0,
        "cached_field_present": "cached_tokens" in details,
        "completion": usage.get("completion_tokens"),
        "latency": dt,
        "id": body.get("id"),
    }


def main() -> int:
    ap = argparse.ArgumentParser(description="前缀缓存命中率探测")
    ap.add_argument("--base-url", default=os.environ.get("UPSTREAM_BASE_URL", ""),
                    help="例如 https://api.vendor.com 或 https://api.vendor.com/v1")
    ap.add_argument("--api-key", default=os.environ.get("UPSTREAM_API_KEY", ""))
    ap.add_argument("--model", required=True)
    ap.add_argument("--prefix-tokens", type=int, default=8000,
                    help="目标前缀 token 数（按 1.086 字符/token 估算）")
    ap.add_argument("--chars-per-token", type=float, default=1.086)
    ap.add_argument("--max-tokens-field", default="max_tokens",
                    choices=["max_tokens", "max_completion_tokens"])
    ap.add_argument("--max-tokens", type=int, default=32, help="输出预算，尽量小以降低成本")
    ap.add_argument("--interval", type=int, default=10, help="两次请求之间的等待秒数")
    ap.add_argument("--long-wait", type=int, default=60, help="最后一次持久性验证的等待秒数")
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--json", action="store_true", help="以 JSON 输出结果，便于回传")
    args = ap.parse_args()

    if not args.base_url or not args.api_key:
        ap.error("需要 --base-url 与 --api-key（或环境变量 UPSTREAM_BASE_URL / UPSTREAM_API_KEY）")

    base = args.base_url.rstrip("/")
    headers = {"Authorization": f"Bearer {args.api_key}", "Content-Type": "application/json"}

    # 自动补 /v1（部分站点把 API 挂在 /v1 下）
    probe = call(base, headers, args.model, "hi", args.max_tokens_field, 8, 60)
    if not probe["ok"]:
        alt = base + "/v1"
        alt_probe = call(alt, headers, args.model, "hi", args.max_tokens_field, 8, 60)
        if alt_probe["ok"]:
            base = alt

    prefix = gen_prefix(int(args.prefix_tokens * args.chars_per_token),
                        seed=random.randint(1, 10**9))
    q1 = "\n\n请总结第一段。"
    q2 = "\n\n请总结最后一段。"

    plan = [
        ("1 冷请求",        prefix + q1, 0),
        ("2 同 body 重发",  prefix + q1, args.interval),
        ("3 换尾部",        prefix + q2, args.interval),
        ("4 长时间后重发",  prefix + q2, args.long_wait),
    ]

    rows = []
    print(f"目标服务: {base}")
    print(f"模型: {args.model}    前缀目标: {args.prefix_tokens} token")
    print(f"输出预算: {args.max_tokens_field}={args.max_tokens}    间隔: {args.interval}s / {args.long_wait}s\n")
    print(f"{'步骤':16s}{'prompt':>9s}{'cached':>9s}{'命中率':>9s}{'耗时':>8s}")
    print("-" * 54)

    for label, content, wait in plan:
        if wait:
            time.sleep(wait)
        res = call(base, headers, args.model, content, args.max_tokens_field,
                   args.max_tokens, args.timeout)
        if not res["ok"]:
            print(f"{label:16s}{'—':>9s}{'—':>9s}{'—':>9s}  {res['err']}")
            rows.append({"step": label, "error": res["err"]})
            continue
        p, c = res["prompt"], res["cached"]
        rate = (c / p * 100) if p else 0.0
        print(f"{label:16s}{p:>9d}{c:>9d}{rate:>8.1f}%{res['latency']:>7.1f}s")
        rows.append({"step": label, "prompt_tokens": p, "cached_tokens": c,
                     "hit_pct": round(rate, 1), "latency_s": round(res["latency"], 2),
                     "cached_field_present": res["cached_field_present"],
                     "id": res["id"]})

    print("-" * 54)
    ok_rows = [r for r in rows if "hit_pct" in r]
    # 第 1 次是冷请求，本来就该是 0，排除在命中率统计之外，否则会把平均值拉低。
    cold = ok_rows[0] if ok_rows else None
    warm = ok_rows[1:]
    w_nonzero = [r for r in warm if r["cached_tokens"] > 0]
    w_avg = (sum(r["hit_pct"] for r in warm) / len(warm)) if warm else 0.0

    if not ok_rows:
        verdict = "全部请求失败，无法判断"
    elif not warm:
        verdict = "请求次数不足，至少需要 2 次才能判断"
    elif not w_nonzero:
        verdict = ("未命中：冷请求之后的所有请求 cached_tokens 均为 0（原始值见上表，非阈值过滤）。"
                   "该接口可能未启用前缀缓存，或未在响应中透出统计")
    elif w_avg >= 90:
        verdict = f"缓存有效：冷请求后平均命中率 {w_avg:.1f}%"
    else:
        verdict = (f"有命中但命中率偏低：{len(w_nonzero)}/{len(warm)} 次非零，"
                   f"平均 {w_avg:.1f}%（可能前缀过短、块对齐粒度较大，或缓存已过期）")

    if cold:
        note = "预期为 0" if cold["cached_tokens"] == 0 else "非 0，说明前缀此前已存在"
        print(f"冷请求: cached={cold['cached_tokens']}（{note}），不计入下列统计")
    print(f"暖请求非零命中: {len(w_nonzero)}/{len(warm)}    平均命中率: {w_avg:.1f}%")
    print(f"结论: {verdict}")

    if args.json:
        print("\n--- JSON ---")
        print(json.dumps({"base_url": base, "model": args.model,
                          "prefix_tokens_target": args.prefix_tokens,
                          "rows": rows, "warm_nonzero_hits": len(w_nonzero), "warm_avg_hit_pct": round(w_avg, 1), "verdict": verdict},
                         ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
