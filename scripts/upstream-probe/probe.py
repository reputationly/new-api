#!/usr/bin/env python3
"""上游供应商直连探针（Phase A）。

不经过 new-api 网关，直接打供应商接口，把指纹 / 能力 / 并发 / TPM 的原始数据
落成 JSON + Markdown 报告，用于回填 docs/供应商测试记录/<vendor>/01-直连测试.md 的结果表。

用法：
    export UPSTREAM_API_KEY=sk-xxx
    python3 scripts/upstream-probe/probe.py \
        --base-url https://api.vendor.com/v1 \
        --model vendor-model-v1 \
        --suites smoke,fingerprint,tools,params,limits

    # 只跑并发 + TPM
    python3 scripts/upstream-probe/probe.py --base-url ... --model ... \
        --suites load --concurrency 1,5,10,20,50 --load-seconds 300

    # 不打真实上游，用内置 mock 验证探针自身
    python3 scripts/upstream-probe/probe.py --self-test

报告产物：results/<时间戳>/report.md、raw.json、cases.json
"""
from __future__ import annotations

import argparse
import json
import os
import random
import re
import statistics
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import requests

# --------------------------------------------------------------------------
# 固定测试语料
# --------------------------------------------------------------------------

# A2-01 tokenizer 指纹：不同模型族的 tokenizer 对这些文本的计数差异很大。
TOKENIZER_TEXTS: list[tuple[str, str]] = [
    ("zh-short", "今天天气不错。"),
    ("zh-long", "模型服务接入测试需要覆盖协议兼容性、计费口径、并发容量和故障语义四个方面，"
                "任何一项缺失都会在正式上线后以对账差额或线上事故的形式暴露出来。"),
    ("en-short", "The quick brown fox jumps over the lazy dog."),
    ("en-long", "Tokenization differences between model families are large enough that a fixed "
                "corpus of text produces systematically different prompt token counts, which in "
                "turn makes per-request billing comparisons meaningful."),
    ("mixed", "把 prompt_tokens 和 completion_tokens 分开统计，才能定位 diff 的来源。"),
    ("code-py", "def f(x):\n    return [i * x for i in range(10) if i % 2 == 0]\n"),
    ("code-go", "func main() {\n\tfmt.Println(\"hello\")\n}\n"),
    ("json", '{"model":"x","messages":[{"role":"user","content":"hi"}],"temperature":0}'),
    ("emoji", "启动测试 🚀🔥✅ 然后看结果 📊"),
    ("digits", "1234567890 0987654321 31415926535897932384626433832795"),
    ("punct", "！！！？？？。。。，，，；；；：：："),
    ("repeat", "测试" * 100),
    ("spaces", "a" + " " * 200 + "b"),
    ("newlines", "line\n" * 100),
    ("url", "https://example.com/path/to/resource?query=1&other=2#fragment"),
    ("uuid", "550e8400-e29b-41d4-a716-446655440000 " * 10),
    ("cjk-punct", "根据《测试方案》第 3.2 节的规定，需要在 T+1 日内完成对账。"),
    ("rare", "饕餮魑魅魍魉耄耋蹙額龃龉"),
    ("one-token", "a"),
    ("long-4k", ("上游模型服务的稳定性需要长时间压测才能验证。" * 200)),
]

# A2-03 确定性指纹：高区分度、格式约束明确的 prompt。
FINGERPRINT_PROMPTS: list[str] = [
    "列出 1 到 20 之间的所有质数，用英文逗号分隔，不要任何解释。",
    "把这句话翻译成英文，只输出译文：上游服务的计费口径必须在上线前确认。",
    "写一个 Python 函数，输入列表返回去重后的升序列表，只输出代码块。",
    "13 乘以 17 等于多少？只输出数字。",
    "用三个字概括：延迟、吞吐、成本。",
    "输出 JSON：{\"ok\": true}，不要多余内容。",
    "把 'hello world' 反转，只输出结果。",
    "列出中国四大发明，每行一个，不要编号。",
    "计算 2 的 10 次方，只输出数字。",
    "用一句话说明什么是 token。",
    "写一个正则，匹配形如 2026-10-20 的日期，只输出正则。",
    "把 'a,b,c' 按逗号拆成 JSON 数组，只输出数组。",
    "输出 5 个英文单词，全部小写，空格分隔。",
    "这三个数排序：9, 3, 7。只输出结果。",
    "用一句话解释 MoE 架构。",
    "输出 SQL：查询 users 表中 id 大于 10 的记录。",
    "把 '测试' 的拼音写出来，只输出拼音。",
    "100 减 37 等于多少？只输出数字。",
    "写一个 bash 命令列出当前目录文件，只输出命令。",
    "用不超过 10 个字描述你的能力边界。",
]

TOOLS: list[dict] = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "查询指定城市的当前天气",
            "parameters": {
                "type": "object",
                "properties": {
                    "city": {"type": "string", "description": "城市名，中文"},
                    "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
                },
                "required": ["city"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "search_orders",
            "description": "按条件查询订单列表",
            "parameters": {
                "type": "object",
                "properties": {
                    "status": {"type": "string", "enum": ["paid", "pending", "refunded"]},
                    "filters": {
                        "type": "object",
                        "properties": {
                            "min_amount": {"type": "number"},
                            "tags": {"type": "array", "items": {"type": "string"}},
                        },
                        "required": ["min_amount"],
                    },
                    "limit": {"type": "integer"},
                },
                "required": ["status", "filters"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "send_email",
            "description": "发送邮件",
            "parameters": {
                "type": "object",
                "properties": {
                    "to": {"type": "string"},
                    "subject": {"type": "string"},
                    "body": {"type": "string"},
                },
                "required": ["to", "subject", "body"],
            },
        },
    },
]

NEEDLE_FILLER = (
    "这是一段用于填充上下文的无关文本，目的是把目标信息推到长上下文的中间位置。"
    "模型服务在长上下文下的检索能力会随长度下降，这是判断是否被量化或截断的重要信号。"
)


def filler_for_tokens(target_tokens: int) -> str:
    """按经验值拼出约 target_tokens 的填充文本（中文约 1.2 字符/token）。

    只是粗对齐，实际 token 数以响应里的 prompt_tokens 为准。
    """
    chars_wanted = max(1, int(target_tokens * 1.2))
    unit = NEEDLE_FILLER
    units = max(1, chars_wanted // len(unit) + 1)
    return (unit * units)[:chars_wanted]


# --------------------------------------------------------------------------
# 基础工具
# --------------------------------------------------------------------------

def pct(vals: list[float], p: float) -> float | None:
    if not vals:
        return None
    s = sorted(vals)
    if len(s) == 1:
        return s[0]
    k = (len(s) - 1) * p / 100.0
    f = int(k)
    c = min(f + 1, len(s) - 1)
    return s[f] + (s[c] - s[f]) * (k - f)


def mask_key(key: str) -> str:
    if not key:
        return "<empty>"
    if len(key) <= 8:
        return "***"
    return key[:4] + "***" + key[-4:]


def validate(instance: Any, schema: dict, path: str = "$") -> list[str]:
    """极简 JSON Schema 校验：只覆盖 type/required/properties/items/enum。"""
    errs: list[str] = []
    t = schema.get("type")
    if t == "object":
        if not isinstance(instance, dict):
            return [f"{path}: 期望 object，实际 {type(instance).__name__}"]
        for k in schema.get("required", []):
            if k not in instance:
                errs.append(f"{path}: 缺少必填 {k}")
        for k, sub in (schema.get("properties") or {}).items():
            if k in instance:
                errs.extend(validate(instance[k], sub, f"{path}.{k}"))
    elif t == "array":
        if not isinstance(instance, list):
            return [f"{path}: 期望 array，实际 {type(instance).__name__}"]
        if "items" in schema:
            for i, v in enumerate(instance):
                errs.extend(validate(v, schema["items"], f"{path}[{i}]"))
    elif t == "string":
        if not isinstance(instance, str):
            errs.append(f"{path}: 期望 string")
        elif "enum" in schema and instance not in schema["enum"]:
            errs.append(f"{path}: {instance!r} 不在 enum {schema['enum']}")
    elif t == "integer":
        if isinstance(instance, bool) or not isinstance(instance, int):
            errs.append(f"{path}: 期望 integer")
    elif t == "number":
        if isinstance(instance, bool) or not isinstance(instance, (int, float)):
            errs.append(f"{path}: 期望 number")
    elif t == "boolean":
        if not isinstance(instance, bool):
            errs.append(f"{path}: 期望 boolean")
    return errs


def norm_usage(u: dict | None) -> dict:
    """把 OpenAI / Anthropic 两种 usage 形状归一。"""
    if not u:
        return {}
    ptd = u.get("prompt_tokens_details") or u.get("input_tokens_details") or {}
    ctd = u.get("completion_tokens_details") or {}
    prompt = u.get("prompt_tokens")
    if prompt is None:
        prompt = u.get("input_tokens")
    completion = u.get("completion_tokens")
    if completion is None:
        completion = u.get("output_tokens")
    return {
        "prompt": prompt,
        "completion": completion,
        "total": u.get("total_tokens"),
        "cache_read": ptd.get("cached_tokens") or u.get("prompt_cache_hit_tokens"),
        "cache_write": ptd.get("cache_creation_tokens") or u.get("cache_creation_input_tokens"),
        "reasoning": ctd.get("reasoning_tokens"),
        "raw": u,
    }


def norm_resp_usage(u: dict | None) -> dict:
    """Responses 协议（/v1/responses）的 usage 形状。"""
    if not u:
        return {}
    itd = u.get("input_tokens_details") or {}
    otd = u.get("output_tokens_details") or {}
    return {
        "prompt": u.get("input_tokens"),
        "completion": u.get("output_tokens"),
        "total": u.get("total_tokens"),
        "cache_read": itd.get("cached_tokens"),
        "cache_write": itd.get("cache_write_tokens"),
        "reasoning": otd.get("reasoning_tokens"),
        "raw": u,
    }


class Case:
    def __init__(self, cid: str, suite: str, title: str, judge: str):
        self.id = cid
        self.suite = suite
        self.title = title
        self.judge = judge
        self.status = "skip"
        self.data: dict = {}
        self.note = ""

    def ok(self, data: dict | None = None, note: str = "") -> "Case":
        self.status = "pass"
        self.data = data or {}
        self.note = note
        return self

    def fail(self, data: dict | None = None, note: str = "") -> "Case":
        self.status = "fail"
        self.data = data or {}
        self.note = note
        return self

    def skip(self, note: str = "") -> "Case":
        self.status = "skip"
        self.note = note
        return self

    def error(self, note: str) -> "Case":
        self.status = "error"
        self.note = note
        return self

    def to_dict(self) -> dict:
        return {
            "id": self.id, "suite": self.suite, "title": self.title,
            "judge": self.judge, "status": self.status,
            "data": self.data, "note": self.note,
        }


# --------------------------------------------------------------------------
# 上游客户端
# --------------------------------------------------------------------------

class Upstream:
    def __init__(self, base_url: str, api_key: str, timeout: int = 120,
                 auth_header: str = "Authorization", auth_prefix: str = "Bearer ",
                 maxtok_field: str = "max_tokens", budget_floor: int = 0,
                 temperature: float | None = None):
        self.base = base_url.rstrip("/")
        self.key = api_key
        self.timeout = timeout
        self.maxtok_field = maxtok_field
        self.budget_floor = budget_floor
        # 有些模型只接受固定温度（例如 Kimi K3 只允许 temperature=1），
        # 统一在这里覆盖，避免 33 处调用点各改一遍。
        self.temperature_override = temperature
        self.headers = {
            "Content-Type": "application/json",
            auth_header: f"{auth_prefix}{api_key}" if api_key else "",
        }
        self.session = requests.Session()
        self.resolved_base = self.base

    def resolve_base(self) -> str:
        """容忍 base_url 少写 /v1。

        只认「200 且返回 {data: [...]}」的端点：不少站点对任意路径都返回 200 的
        前端页面，光看状态码会把 SPA 的 HTML 当成 API。
        """
        for cand in [self.base, self.base + "/v1"]:
            try:
                r = self.session.get(f"{cand}/models", headers=self.headers, timeout=20)
                if r.status_code != 200:
                    continue
                try:
                    body = r.json()
                except ValueError:
                    continue
                if isinstance(body, dict) and isinstance(body.get("data"), list):
                    self.resolved_base = cand
                    return cand
            except requests.RequestException:
                continue
        self.resolved_base = self.base
        return self.base

    def models(self) -> tuple[int, dict]:
        r = self.session.get(f"{self.resolved_base}/models", headers=self.headers, timeout=30)
        try:
            body = r.json()
        except ValueError:
            body = {"_text": r.text[:500]}
        return r.status_code, body

    def chat(self, model: str, messages: list[dict], stream: bool = False,
             extra: dict | None = None, keep_budget: bool = False,
             literal_maxtok: bool = False) -> dict:
        payload = {"model": model, "messages": messages}
        if stream:
            payload["stream"] = True
        if extra:
            payload.update(extra)

        # 固定温度模型（如 Kimi K3 只允许 1）需要统一改写，否则用例全会 400。
        if self.temperature_override is not None and "temperature" in payload:
            payload["temperature"] = self.temperature_override

        # 有的供应商忽略 max_tokens、只认 max_completion_tokens（推理 token 也算在
        # 这个预算里）。统一改写字段，避免用例里到处写两套。
        field = self.maxtok_field
        if not literal_maxtok and field != "max_tokens" and "max_tokens" in payload:
            payload[field] = payload.pop("max_tokens")
        # 推理模型会先消耗预算再出正文，预算太小会只剩空 content。给需要正文的
        # 用例抬一个地板，测量型用例（tokenizer / max_tokens 上限）用 keep_budget 跳过。
        if not keep_budget and self.budget_floor and field in payload \
                and payload[field] < self.budget_floor:
            payload[field] = self.budget_floor
        if stream:
            payload.setdefault("stream_options", {"include_usage": True})

        t0 = time.perf_counter()
        out: dict = {
            "status": None, "latency": None, "ttft": None, "text": "",
            "tool_calls": [], "usage": {}, "finish_reason": None,
            "model_echo": None, "id": None, "system_fingerprint": None,
            "headers": {}, "error": None, "stream": stream,
        }
        try:
            if stream:
                r = self.session.post(f"{self.resolved_base}/chat/completions",
                                      headers=self.headers, json=payload,
                                      timeout=self.timeout, stream=True)
                out["status"] = r.status_code
                out["headers"] = dict(r.headers)
                if r.status_code != 200:
                    out["error"] = r.text[:800]
                    out["latency"] = time.perf_counter() - t0
                    return out
                slots: dict[int, dict] = {}
                ttft = None
                for raw in r.iter_lines(decode_unicode=False):
                    if not raw:
                        continue
                    line = raw.decode("utf-8", "replace").strip()
                    if not line.startswith("data:"):
                        continue
                    payload_str = line[5:].strip()
                    if payload_str == "[DONE]":
                        break
                    try:
                        obj = json.loads(payload_str)
                    except ValueError:
                        continue
                    if obj.get("usage"):
                        out["usage"] = norm_usage(obj["usage"])
                    out["id"] = out["id"] or obj.get("id")
                    out["model_echo"] = out["model_echo"] or obj.get("model")
                    if obj.get("system_fingerprint"):
                        out["system_fingerprint"] = obj["system_fingerprint"]
                    for ch in obj.get("choices") or []:
                        out["finish_reason"] = ch.get("finish_reason") or out["finish_reason"]
                        d = ch.get("delta") or {}
                        if d.get("content"):
                            if ttft is None:
                                ttft = time.perf_counter() - t0
                            out["text"] += d["content"]
                        for tc in d.get("tool_calls") or []:
                            idx = tc.get("index", 0)
                            slot = slots.setdefault(idx, {"id": "", "name": "", "arguments": ""})
                            if tc.get("id"):
                                slot["id"] = slot["id"] or tc["id"]
                            fn = tc.get("function") or {}
                            if fn.get("name"):
                                slot["name"] = slot["name"] or fn["name"]
                            if fn.get("arguments"):
                                slot["arguments"] += fn["arguments"]
                out["tool_calls"] = [slots[i] for i in sorted(slots)]
                out["ttft_raw"] = ttft
            else:
                r = self.session.post(f"{self.resolved_base}/chat/completions",
                                      headers=self.headers, json=payload, timeout=self.timeout)
                out["status"] = r.status_code
                out["headers"] = dict(r.headers)
                try:
                    body = r.json()
                except ValueError:
                    body = None
                    out["error"] = r.text[:800]
                if r.status_code != 200:
                    out["error"] = json.dumps(body, ensure_ascii=False)[:800] if body else out["error"]
                    out["latency"] = time.perf_counter() - t0
                    return out
                if body:
                    out["id"] = body.get("id")
                    out["model_echo"] = body.get("model")
                    out["system_fingerprint"] = body.get("system_fingerprint")
                    out["usage"] = norm_usage(body.get("usage"))
                    choices = body.get("choices") or []
                    if choices:
                        msg = choices[0].get("message") or {}
                        out["text"] = msg.get("content") or ""
                        out["finish_reason"] = choices[0].get("finish_reason")
                        for tc in msg.get("tool_calls") or []:
                            fn = tc.get("function") or {}
                            out["tool_calls"].append({
                                "id": tc.get("id", ""),
                                "name": fn.get("name", ""),
                                "arguments": fn.get("arguments", ""),
                            })
                        out["choices_count"] = len(choices)
                        out["choices_text"] = [
                            ((c.get("message") or {}).get("content") or "") for c in choices
                        ]
                    out["logprobs"] = (choices[0].get("logprobs") if choices else None)
        except requests.Timeout:
            out["error"] = f"timeout after {self.timeout}s"
        except requests.RequestException as e:
            out["error"] = f"{type(e).__name__}: {e}"
        out["latency"] = time.perf_counter() - t0
        if out.get("ttft_raw"):
            out["ttft"] = out["ttft_raw"]
        out.pop("ttft_raw", None)
        return out

    def responses(self, model: str, input_text: str, extra: dict | None = None,
                  stream: bool = False) -> dict:
        """POST /v1/responses。事件序列与 chat 不同，单独解析。"""
        payload = {"model": model, "input": input_text}
        if extra:
            payload.update(extra)
        if stream:
            payload["stream"] = True
        out: dict = {
            "status": None, "latency": None, "ttft": None, "text": "",
            "usage": {}, "events": [], "status_field": None,
            "reasoning_summary": "", "error": None, "stream": stream,
            "output_types": [], "raw": None,
        }
        t0 = time.perf_counter()
        try:
            if stream:
                r = self.session.post(f"{self.resolved_base}/responses", headers=self.headers,
                                      json=payload, timeout=self.timeout, stream=True)
                out["status"] = r.status_code
                if r.status_code != 200:
                    out["error"] = r.text[:800]
                    out["latency"] = time.perf_counter() - t0
                    return out
                for raw in r.iter_lines(decode_unicode=False):
                    if not raw:
                        continue
                    line = raw.decode("utf-8", "replace").strip()
                    if line.startswith("event:"):
                        out["events"].append(line[6:].strip())
                        continue
                    if not line.startswith("data:"):
                        continue
                    try:
                        obj = json.loads(line[5:].strip())
                    except ValueError:
                        continue
                    typ = obj.get("type") or ""
                    if typ in ("response.output_text.delta",):
                        if out["ttft"] is None:
                            out["ttft"] = time.perf_counter() - t0
                        out["text"] += obj.get("delta") or ""
                    elif typ == "response.reasoning_summary_text.delta":
                        out["reasoning_summary"] += obj.get("delta") or ""
                    elif typ in ("response.completed", "response.incomplete", "response.failed"):
                        resp = obj.get("response") or {}
                        out["status_field"] = resp.get("status") or typ
                        if resp.get("usage"):
                            out["usage"] = norm_resp_usage(resp["usage"])
                        out["output_types"] = [i.get("type") for i in (resp.get("output") or [])]
                        out["events"].append(typ)
            else:
                r = self.session.post(f"{self.resolved_base}/responses", headers=self.headers,
                                      json=payload, timeout=self.timeout)
                out["status"] = r.status_code
                try:
                    body = r.json()
                except ValueError:
                    out["error"] = r.text[:800]
                    out["latency"] = time.perf_counter() - t0
                    return out
                out["raw"] = body
                if r.status_code != 200:
                    out["error"] = json.dumps(body, ensure_ascii=False)[:800]
                else:
                    out["status_field"] = body.get("status")
                    out["usage"] = norm_resp_usage(body.get("usage") or {})
                    for item in body.get("output") or []:
                        ityp = item.get("type")
                        out["output_types"].append(ityp)
                        if ityp == "message":
                            for cc in item.get("content") or []:
                                if cc.get("type") == "output_text":
                                    out["text"] += cc.get("text") or ""
                        elif ityp == "reasoning":
                            for sm in item.get("summary") or []:
                                out["reasoning_summary"] += sm.get("text") or ""
        except requests.Timeout:
            out["error"] = f"timeout after {self.timeout}s"
        except requests.RequestException as e:
            out["error"] = f"{type(e).__name__}: {e}"
        out["latency"] = time.perf_counter() - t0
        return out


# --------------------------------------------------------------------------
# Suites
# --------------------------------------------------------------------------

def suite_smoke(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]

    c = Case("A1-01", "A1 冒烟与元数据", "模型列表可获取", "HTTP 200 且返回 model 数组")
    code, body = u.models()
    names = [m.get("id") for m in (body.get("data") or [])] if isinstance(body, dict) else []
    (c.ok if code == 200 else c.fail)({"status": code, "count": len(names), "models": names[:50],
                                       "raw": body if code != 200 else None})
    out.append(c)

    c = Case("A1-02", "A1 冒烟与元数据", "非流式 chat", "200 且 usage 三项齐全")
    r = u.chat(model, [{"role": "user", "content": "只回复两个字：收到"}],
               extra={"max_tokens": 32, "temperature": 0})
    usage_ok = all(r["usage"].get(k) is not None for k in ("prompt", "completion"))
    (c.ok if r["status"] == 200 and usage_ok else c.fail)(
        {"status": r["status"], "latency_ms": round(r["latency"] * 1000),
         "usage": r["usage"].get("raw"), "text": r["text"][:200], "error": r["error"]})
    ctx["baseline_usage"] = r["usage"]

    out.append(c)

    c = Case("A1-03", "A1 冒烟与元数据", "流式 chat + 末帧 usage", "SSE 正常结束且 usage 存在")
    r = u.chat(model, [{"role": "user", "content": "数到五，用空格分隔"}],
               stream=True, extra={"max_tokens": 64, "temperature": 0})
    (c.ok if r["status"] == 200 and r["usage"].get("prompt") is not None else c.fail)(
        {"status": r["status"], "ttft_ms": round((r["ttft"] or 0) * 1000),
         "latency_ms": round(r["latency"] * 1000), "usage": r["usage"].get("raw"),
         "text": r["text"][:200], "error": r["error"]})
    out.append(c)

    c = Case("A1-04", "A1 冒烟与元数据", "多轮上下文", "第二轮能引用第一轮信息")
    r = u.chat(model, [
        {"role": "user", "content": "记住数字 42"},
        {"role": "assistant", "content": "好的，我记住了 42。"},
        {"role": "user", "content": "我刚让你记的数字是多少？只输出数字。"},
    ], extra={"max_tokens": 32, "temperature": 0})
    (c.ok if r["status"] == 200 and "42" in r["text"] else c.fail)(
        {"status": r["status"], "text": r["text"][:200], "error": r["error"]})
    out.append(c)

    c = Case("A1-05", "A1 冒烟与元数据", "错误 key 的返回", "4xx 且错误体可解析、不泄露堆栈")
    bad = Upstream(u.base, "sk-invalid-probe-key", u.timeout)
    bad.resolved_base = u.resolved_base
    r = bad.chat(model, [{"role": "user", "content": "hi"}], extra={"max_tokens": 8})
    (c.ok if (r["status"] or 0) in (401, 403) else c.fail)(
        {"status": r["status"], "error": (r["error"] or "")[:300]})
    out.append(c)

    c = Case("A1-06", "A1 冒烟与元数据", "不存在的模型", "4xx 且错误信息可读")
    r = u.chat("__no_such_model_probe__", [{"role": "user", "content": "hi"}], extra={"max_tokens": 8})
    (c.ok if 400 <= (r["status"] or 0) < 500 else c.fail)(
        {"status": r["status"], "error": (r["error"] or "")[:300]})
    out.append(c)

    c = Case("A1-07", "A1 冒烟与元数据", "响应元数据采集", "记录 id/model/头，供指纹与排障使用")
    r = u.chat(model, [{"role": "user", "content": "ping"}], extra={"max_tokens": 8})
    hdrs = {k: v for k, v in (r["headers"] or {}).items()
            if k.lower() in ("server", "x-request-id", "x-ratelimit-limit-requests",
                             "x-ratelimit-remaining-requests", "retry-after", "date")}
    (c.ok if r["status"] == 200 else c.fail)(
        {"response_id": r["id"], "model_echo": r["model_echo"],
         "system_fingerprint": r["system_fingerprint"], "headers": hdrs})
    out.append(c)
    return out


def suite_fingerprint(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]

    # A2-01 tokenizer 计数
    c = Case("A2-01", "A2 模型指纹", "tokenizer 计数（20 段固定文本）",
             "与官方参考逐条比较；差值应稳定且 ≤1%，固定偏移需记录")
    counts = []
    for cid, text in TOKENIZER_TEXTS:
        r = u.chat(model, [{"role": "user", "content": text}],
                   extra={"max_tokens": 1, "temperature": 0}, keep_budget=True)
        counts.append({"case": cid, "chars": len(text),
                       "prompt_tokens": r["usage"].get("prompt"),
                       "status": r["status"], "error": (r["error"] or "")[:120]})
    got = [x for x in counts if x["prompt_tokens"]]
    c.ok({"counts": counts,
          "note": "语料是固定的：对官方 API 再跑一次本套件，把两份 A2-01 的 prompt_tokens "
                  "逐条相减即为 tokenizer 偏差"},
         note=f"{len(got)}/{len(counts)} 条拿到计数")
    if not got:
        c.fail({"counts": counts}, note="全部没拿到 prompt_tokens")
    out.append(c)

    # A2-02 缓存语义
    c = Case("A2-02", "A2 模型指纹", "prompt_tokens 是否包含缓存命中",
             "两次同前缀请求：若第二次计数明显下降或出现 cache 字段，记录口径")
    long_ctx = "以下是背景资料，请忽略其内容。" + ("无关背景文本。" * 300)
    r1 = u.chat(model, [{"role": "system", "content": long_ctx},
                        {"role": "user", "content": "第一个问题，只输出 A"}],
                extra={"max_tokens": 8, "temperature": 0})
    time.sleep(1.0)
    r2 = u.chat(model, [{"role": "system", "content": long_ctx},
                        {"role": "user", "content": "第二个问题，只输出 B"}],
                extra={"max_tokens": 8, "temperature": 0})
    p1, p2 = r1["usage"].get("prompt"), r2["usage"].get("prompt")
    verdict = "unknown"
    if p1 and p2:
        if p2 < p1 * 0.9:
            verdict = "excludes-cache（第二次计数明显下降，prompt 不含缓存）"
        elif r2["usage"].get("cache_read"):
            verdict = "includes-cache（计数不降但单独报了 cache 字段）"
        else:
            verdict = "no-cache-signal（未见缓存生效，可能未命中或未支持）"
    c.ok({"first_prompt_tokens": p1, "second_prompt_tokens": p2,
          "cache_read_1": r1["usage"].get("cache_read"),
          "cache_read_2": r2["usage"].get("cache_read"),
          "verdict": verdict})
    if not p1 or not p2:
        c.fail(note="请求失败，无法判断")
    out.append(c)

    # A2-03 确定性
    c = Case("A2-03", "A2 模型指纹", "temperature=0 的确定性（20 prompt × 5 次）",
             "每 prompt ≥4/5 次首 32 token 一致才可作指纹；记录整体稳定率")
    stable = 0
    rows = []
    for i, prompt in enumerate(FINGERPRINT_PROMPTS):
        heads, fulls = [], []
        for _ in range(5):
            r = u.chat(model, [{"role": "user", "content": prompt}],
                       extra={"max_tokens": 64, "temperature": 0, "seed": 42})
            txt = r["text"] or ""
            if txt:
                heads.append(txt[:32])
            fulls.append(txt)
        best = max(heads.count(h) for h in heads) if heads else 0
        stable += 1 if best >= 4 else 0
        rows.append({"idx": i, "prompt": prompt[:40], "best_of_5": best,
                     "empty_samples": sum(1 for f in fulls if not f),
                     "sample": fulls[0][:120]})
    rate = stable / len(FINGERPRINT_PROMPTS)
    c.ok({"stable_prompts": stable, "total": len(FINGERPRINT_PROMPTS),
          "stable_rate": round(rate, 3), "rows": rows},
         note="稳定率低说明该模型非确定性高，指纹只能用 logprobs 或统计特征")
    out.append(c)

    # A2-04 性能基线
    c = Case("A2-04", "A2 模型指纹", "单请求性能基线", "记录 TTFT 与输出速度，与同模型其他渠道对比量级")
    lat, ttfts, speeds, toks = [], [], [], []
    for _ in range(10):
        r = u.chat(model, [{"role": "user", "content": "写一段 100 字左右的说明，主题是缓存。"}],
                   stream=True, extra={"max_tokens": 256, "temperature": 0})
        if r["status"] == 200:
            lat.append(r["latency"])
            if r["ttft"]:
                ttfts.append(r["ttft"])
            comp = r["usage"].get("completion")
            if comp and r["latency"] > 0:
                toks.append(comp)
                gen = r["latency"] - (r["ttft"] or 0)
                if gen > 0:
                    speeds.append(comp / gen)
    c.ok({"n": len(lat),
          "latency_p50": round(pct(lat, 50) or 0, 3),
          "latency_p95": round(pct(lat, 95) or 0, 3),
          "ttft_p50_ms": round((pct(ttfts, 50) or 0) * 1000),
          "ttft_p95_ms": round((pct(ttfts, 95) or 0) * 1000),
          "tokens_per_sec_p50": round(pct(speeds, 50) or 0, 1),
          "completion_tokens_p50": round(pct([float(x) for x in toks], 50) or 0)})
    if not lat:
        c.fail(note="10 次全部失败")
    out.append(c)

    # A2-05 模型回显
    c = Case("A2-05", "A2 模型指纹", "model 字段回显", "回显应与请求一致；不一致可能是静默替换")
    r = u.chat(model, [{"role": "user", "content": "ping"}], extra={"max_tokens": 4})
    echo = r["model_echo"]
    (c.ok if echo == model else c.fail)(
        {"requested": model, "echo": echo, "response_id": r["id"],
         "system_fingerprint": r["system_fingerprint"]})
    out.append(c)
    return out


def suite_tools(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]

    def first_tool_call(r: dict) -> dict | None:
        return r["tool_calls"][0] if r["tool_calls"] else None

    def args_json(tc: dict | None) -> tuple[dict | None, str | None]:
        if not tc:
            return None, "没有 tool_calls"
        try:
            return json.loads(tc.get("arguments") or ""), None
        except ValueError as e:
            return None, f"arguments 不是合法 JSON: {e}"

    # A3-01 单次调用
    c = Case("A3-01", "A3 service tools", "单次工具调用", "返回 tool_calls，name 正确，arguments 合法 JSON")
    r = u.chat(model, [{"role": "user", "content": "北京现在天气怎么样？请调用工具查询。"}],
               extra={"tools": TOOLS[:1], "max_tokens": 256, "temperature": 0})
    tc = first_tool_call(r)
    args, err = args_json(tc)
    ok = r["status"] == 200 and tc is not None and tc.get("name") == "get_weather" and args is not None
    (c.ok if ok else c.fail)({"status": r["status"], "tool_calls": r["tool_calls"],
                              "text": r["text"][:200], "error": err or r["error"]})
    out.append(c)

    # A3-02 多轮闭环
    c = Case("A3-02", "A3 service tools", "多轮闭环", "回填工具结果后能给出最终答案")
    msgs = [{"role": "user", "content": "北京现在天气怎么样？请调用工具查询。"}]
    r1 = u.chat(model, msgs, extra={"tools": TOOLS[:1], "max_tokens": 256, "temperature": 0})
    tc = first_tool_call(r1)
    if tc:
        msgs.append({"role": "assistant", "content": None,
                     "tool_calls": [{"id": tc.get("id") or "call_1", "type": "function",
                                     "function": {"name": tc["name"], "arguments": tc["arguments"]}}]})
        msgs.append({"role": "tool", "tool_call_id": tc.get("id") or "call_1",
                     "content": '{"temperature": 18, "unit": "celsius", "condition": "晴"}'})
        r2 = u.chat(model, msgs, extra={"tools": TOOLS[:1], "max_tokens": 256, "temperature": 0})
        ok = r2["status"] == 200 and bool(r2["text"]) and not r2["tool_calls"]
        (c.ok if ok else c.fail)({"second_status": r2["status"], "answer": r2["text"][:200],
                                  "error": r2["error"]})
    else:
        c.fail({"first_status": r1["status"]}, note="第一轮没有产生 tool_calls")
    out.append(c)

    # A3-03 并行调用
    c = Case("A3-03", "A3 service tools", "并行工具调用", "一次返回 ≥2 个 tool_calls")
    r = u.chat(model, [{"role": "user",
                        "content": "同时做三件事：查北京天气、查已支付且金额大于 100 的订单、"
                                   "给 a@example.com 发一封主题为 test 的邮件。"}],
               extra={"tools": TOOLS, "max_tokens": 512, "temperature": 0,
                      "parallel_tool_calls": True})
    n = len(r["tool_calls"])
    (c.ok if n >= 2 else c.fail)({"tool_call_count": n, "tool_calls": r["tool_calls"],
                                  "text": r["text"][:200]})
    out.append(c)

    # A3-04 强制 tool_choice
    c = Case("A3-04", "A3 service tools", "强制 tool_choice", "必然调用指定工具，不自由发挥")
    r = u.chat(model, [{"role": "user", "content": "你好"}],
               extra={"tools": TOOLS[:1], "max_tokens": 128, "temperature": 0,
                      "tool_choice": {"type": "function", "function": {"name": "get_weather"}}})
    tc = first_tool_call(r)
    (c.ok if tc and tc.get("name") == "get_weather" else c.fail)(
        {"status": r["status"], "tool_calls": r["tool_calls"], "text": r["text"][:200]})
    out.append(c)

    # A3-05 tool_choice required
    c = Case("A3-05", "A3 service tools", "tool_choice=required", "至少产生一个 tool_call")
    r = u.chat(model, [{"role": "user", "content": "随便问一句：现在几点？"}],
               extra={"tools": TOOLS, "max_tokens": 128, "temperature": 0,
                      "tool_choice": "required"})
    (c.ok if r["tool_calls"] else c.fail)(
        {"status": r["status"], "tool_calls": r["tool_calls"], "text": r["text"][:200],
         "error": (r["error"] or "")[:200]})
    out.append(c)

    # A3-06 流式分片
    c = Case("A3-06", "A3 service tools", "流式 tool_calls 分片拼接", "拼接后 arguments 是合法 JSON")
    r = u.chat(model, [{"role": "user", "content": "查一下上海的天气。"}],
               stream=True, extra={"tools": TOOLS[:1], "max_tokens": 256, "temperature": 0})
    tc = first_tool_call(r)
    args, err = args_json(tc)
    (c.ok if r["status"] == 200 and tc and args is not None else c.fail)(
        {"status": r["status"], "tool_calls": r["tool_calls"], "error": err or r["error"]})
    out.append(c)

    # A3-07 嵌套 schema 遵从率
    c = Case("A3-07", "A3 service tools", "嵌套 schema 遵从率（20 次）",
             "arguments 通过 schema 校验的比例应 ≥95%")
    schema = TOOLS[1]["function"]["parameters"]
    pass_n, errs, samples = 0, [], []
    for i in range(20):
        r = u.chat(model, [{"role": "user",
                            "content": f"查询已支付、金额大于 {100 + i} 的订单，最多 5 条，标签用 vip。"}],
                   extra={"tools": [TOOLS[1]], "max_tokens": 256, "temperature": 0,
                          "tool_choice": {"type": "function",
                                          "function": {"name": "search_orders"}}})
        tc = first_tool_call(r)
        args, err = args_json(tc)
        if args is None:
            errs.append(err)
            continue
        v = validate(args, schema)
        if v:
            errs.extend(v[:3])
        else:
            pass_n += 1
        if len(samples) < 3:
            samples.append(args)
    rate = pass_n / 20
    (c.ok if rate >= 0.95 else c.fail)(
        {"pass": pass_n, "total": 20, "pass_rate": round(rate, 3),
         "errors": errs[:20], "samples": samples})
    out.append(c)

    # A3-08 长参数
    c = Case("A3-08", "A3 service tools", "长参数不被截断", "2000 字符参数完整回传")
    long_body = "长文本" * 500
    r = u.chat(model, [{"role": "user",
                        "content": f"给 a@example.com 发邮件，主题 truncation-test，"
                                   f"正文严格使用这段内容：{long_body}"}],
               extra={"tools": [TOOLS[2]], "max_tokens": 512, "temperature": 0,
                      "tool_choice": {"type": "function", "function": {"name": "send_email"}}})
    tc = first_tool_call(r)
    args, err = args_json(tc)
    got_len = len((args or {}).get("body", ""))
    (c.ok if args and got_len >= 1000 else c.fail)(
        {"body_len": got_len, "expected_at_least": 1000, "error": err or r["error"]})
    out.append(c)
    return out


def suite_params(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]

    # A4-01 max_tokens
    c = Case("A4-01", "A4 参数与输出格式", "max_tokens 生效", "max_tokens=5 时 completion_tokens ≤5")
    r = u.chat(model, [{"role": "user", "content": "写一段 300 字关于海洋的描述。"}],
               extra={"max_tokens": 5, "temperature": 0}, keep_budget=True)
    comp = r["usage"].get("completion")
    (c.ok if r["status"] == 200 and comp is not None and comp <= 5 else c.fail)(
        {"status": r["status"], "completion_tokens": comp,
         "finish_reason": r["finish_reason"], "text": r["text"][:80], "error": r["error"]},
        note="" if (comp or 0) <= 5 else "max_tokens 被忽略：设 5 却产出更多 token，成本无法用该参数封顶")
    out.append(c)

    # A4-02 stop
    c = Case("A4-02", "A4 参数与输出格式", "stop 生效", "输出在 stop 词处截断")
    r = u.chat(model, [{"role": "user", "content": "依次输出 1 到 20，用逗号分隔。"}],
               extra={"max_tokens": 200, "temperature": 0, "stop": ["5"]})
    txt = r["text"] or ""
    (c.ok if r["status"] == 200 and "5" not in txt else c.fail)(
        {"status": r["status"], "text": txt[:120]})
    out.append(c)

    # A4-03 temperature 方差
    c = Case("A4-03", "A4 参数与输出格式", "temperature 生效", "temp=1 的多样性必须明显高于 temp=0")
    def spread(temp: float) -> float:
        texts = []
        for _ in range(8):
            r = u.chat(model, [{"role": "user", "content": "给一只猫起个名字，只输出名字。"}],
                       extra={"max_tokens": 16, "temperature": temp})
            texts.append(r["text"].strip())
        uniq = len(set(texts))
        return uniq / len(texts)
    s0, s1 = spread(0.0), spread(1.0)
    (c.ok if s1 > s0 else c.fail)({"unique_ratio_temp0": round(s0, 3),
                                   "unique_ratio_temp1": round(s1, 3)})
    out.append(c)

    # A4-04 n>1
    c = Case("A4-04", "A4 参数与输出格式", "n>1 返回多个 choice", "返回 n 个 choice 且按 n 倍计费")
    r = u.chat(model, [{"role": "user", "content": "说一句问候语。"}],
               extra={"max_tokens": 32, "temperature": 1, "n": 3})
    cnt = r.get("choices_count") or len(r.get("choices_text") or [])
    comp = r["usage"].get("completion")
    (c.ok if r["status"] == 200 and cnt == 3 else c.fail)(
        {"status": r["status"], "choices": cnt, "completion_tokens": comp,
         "error": (r["error"] or "")[:200]})
    out.append(c)

    # A4-05 json_object
    c = Case("A4-05", "A4 参数与输出格式", "response_format=json_object", "100% 返回合法 JSON")
    bad = 0
    for _ in range(10):
        r = u.chat(model, [{"role": "user",
                            "content": "把 {name: 张三, age: 30} 转成 JSON 输出，用 json_object 格式。"}],
                   extra={"max_tokens": 128, "temperature": 0,
                          "response_format": {"type": "json_object"}})
        try:
            json.loads(r["text"] or "")
        except ValueError:
            bad += 1
    (c.ok if bad == 0 else c.fail)({"invalid_json": bad, "total": 10})
    out.append(c)

    # A4-06 json_schema
    schema = {
        "type": "object",
        "properties": {
            "name": {"type": "string"},
            "age": {"type": "integer"},
            "tags": {"type": "array", "items": {"type": "string"}},
        },
        "required": ["name", "age"],
        "additionalProperties": False,
    }
    c = Case("A4-06", "A4 参数与输出格式", "response_format=json_schema",
             "输出通过 schema 校验（20 次）")
    pass_n, errs = 0, []
    for _ in range(20):
        r = u.chat(model, [{"role": "user",
                            "content": "虚构一个人物，包含 name、age、tags 三个字段。"}],
                   extra={"max_tokens": 200, "temperature": 0,
                          "response_format": {"type": "json_schema",
                                              "json_schema": {"name": "person", "strict": True,
                                                              "schema": schema}}})
        try:
            obj = json.loads(r["text"] or "")
        except ValueError:
            errs.append("不是合法 JSON")
            continue
        v = validate(obj, schema)
        if v:
            errs.extend(v[:2])
        else:
            pass_n += 1
    (c.ok if pass_n >= 19 else c.fail)(
        {"pass": pass_n, "total": 20, "pass_rate": round(pass_n / 20, 3), "errors": errs[:10]})
    out.append(c)

    # A4-07 logprobs
    c = Case("A4-07", "A4 参数与输出格式", "logprobs 可用", "返回概率值且数值合理（0..1 对数域）")
    r = u.chat(model, [{"role": "user", "content": "输出单词 hello"}],
               extra={"max_tokens": 8, "temperature": 0, "logprobs": True, "top_logprobs": 3})
    lp = r.get("logprobs")
    (c.ok if r["status"] == 200 and lp else c.fail)(
        {"status": r["status"], "logprobs": lp, "error": (r["error"] or "")[:200]})
    out.append(c)

    # A4-08 reasoning 字段
    c = Case("A4-08", "A4 参数与输出格式", "推理字段与 reasoning_tokens",
             "记录是否返回 reasoning_content，以及 usage 是否拆分 reasoning_tokens")
    r = u.chat(model, [{"role": "user", "content": "一个水池进水管 3 小时注满，出水管 5 小时放空，"
                                                 "同时打开多久注满？只给最终答案。"}],
               extra={"max_tokens": 512, "temperature": 0})
    (c.ok)({"status": r["status"], "reasoning_tokens": r["usage"].get("reasoning"),
            "usage": r["usage"].get("raw"), "text": r["text"][:200]},
           note="若 usage 无 reasoning_tokens，说明推理 token 已并入 completion 或未返回")
    out.append(c)

    # A4-09 seed
    c = Case("A4-09", "A4 参数与输出格式", "seed 可复现", "同 seed 多次输出应一致（支持则通过）")
    outs = []
    failed = 0
    for _ in range(4):
        r = u.chat(model, [{"role": "user", "content": "随机输出一个 1 到 100 的整数。"}],
                   extra={"max_tokens": 8, "temperature": 0.7, "seed": 12345})
        if r["status"] != 200:
            failed += 1
            break
        outs.append((r["text"] or "").strip())
    if failed or not outs:
        c.fail({"outputs": outs}, note="请求失败，无法判断 seed 是否生效")
    elif len(set(outs)) == 1:
        c.ok({"outputs": outs})
    else:
        c.fail({"outputs": outs},
               note="同 seed 结果不一致：可能不支持 seed，也可能被批处理打散，需人工确认")
    out.append(c)

    # A4-11 max_tokens 与 max_completion_tokens 的语义差
    c = Case("A4-11", "A4 参数与输出格式", "两个预算字段的语义",
             "记录哪个字段能封顶，以及推理 token 是否计入预算")
    rows = []
    for fld in ("max_tokens", "max_completion_tokens"):
        r = u.chat(model, [{"role": "user", "content": "写一段 300 字关于海洋的描述。"}],
                   extra={fld: 16, "temperature": 0}, keep_budget=True, literal_maxtok=True)
        rows.append({"field": fld, "status": r["status"],
                     "completion_tokens": r["usage"].get("completion"),
                     "reasoning_tokens": r["usage"].get("reasoning"),
                     "finish_reason": r["finish_reason"],
                     "error": (r["error"] or "")[:160]})
    honored = [x["field"] for x in rows
               if x["completion_tokens"] is not None and x["completion_tokens"] <= 16]
    (c.ok if honored else c.fail)(
        {"rows": rows, "honored": honored},
        note=f"生效字段：{honored}" if honored else "两个字段都没封顶")
    out.append(c)
    return out


def suite_limits(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]

    # A5-01 上下文阶梯
    c = Case("A5-01", "A5 边界", f"上下文上限阶梯（{ctx['ladder']}）",
             "记录每档实际 prompt_tokens 与是否报错，最大成功档即实测上限")
    rows = []
    for target in ctx["ladder"]:
        body = filler_for_tokens(target)
        r = u.chat(model, [{"role": "user", "content": body + "\n\n请只回复 OK。"}],
                   extra={"max_tokens": 8, "temperature": 0})
        rows.append({"target_tokens": target, "chars": len(body),
                     "status": r["status"],
                     "actual_prompt_tokens": r["usage"].get("prompt"),
                     "error": (r["error"] or "")[:160]})
        if r["status"] != 200:
            break
    max_ok = max([x["target_tokens"] for x in rows if x["status"] == 200], default=0)
    (c.ok)({"rows": rows, "max_success_target": max_ok,
            "note": "target 是请求规模而非实测 token，实测列以 actual_prompt_tokens 为准"})
    if not max_ok:
        c.fail(note="最小档就失败，检查请求体或鉴权")
    out.append(c)

    # A5-02 max_tokens 阶梯
    c = Case("A5-02", "A5 边界", "max_tokens 上限阶梯", "记录被拒的阈值")
    rows = []
    for mt in [512, 4096, 8192, 16384, 32768, 65536]:
        r = u.chat(model, [{"role": "user", "content": "输出一段话。"}],
                   extra={"max_tokens": mt, "temperature": 0}, keep_budget=True)
        rows.append({"max_tokens": mt, "status": r["status"],
                     "error": (r["error"] or "")[:120]})
        if r["status"] != 200:
            break
    (c.ok)({"rows": rows})
    out.append(c)

    # A5-03 needle
    c = Case("A5-03", "A5 边界", "长上下文检索（needle）", "在长上下文中间插入暗号，应能原样召回")
    rows = []
    for target in [8000, 32000, 64000]:
        needle = f"ZK-{random.randint(100000, 999999)}"
        head = filler_for_tokens(target // 2)
        tail = filler_for_tokens(target // 2)
        body = f"{head}\n暗号是 {needle}，请记住。\n{tail}"
        r = u.chat(model, [{"role": "user", "content": body + "\n\n我让你记住的暗号是什么？只输出暗号。"}],
                   extra={"max_tokens": 32, "temperature": 0})
        hit = needle in (r["text"] or "")
        rows.append({"target_tokens": target, "needle": needle, "status": r["status"],
                     "hit": hit, "answer": (r["text"] or "")[:60],
                     "error": (r["error"] or "")[:120]})
        if r["status"] != 200:
            break
    ok_n = sum(1 for x in rows if x["hit"])
    (c.ok if ok_n == len(rows) and rows else c.fail)(
        {"rows": rows, "hits": ok_n, "total": len(rows)})
    out.append(c)
    return out


def suite_load(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]
    prompt = "固定长度请求：请写一段 200 字左右的说明，主题是并发与限流。" * 3
    extra = {"max_tokens": 200, "temperature": 0}

    rows = []
    for conc in ctx["concurrency"]:
        deadline = time.perf_counter() + ctx["load_seconds"]
        stats: list[dict] = []
        lock = threading.Lock()

        def worker() -> None:
            local = []
            while time.perf_counter() < deadline:
                r = u.chat(model, [{"role": "user", "content": prompt}], extra=extra)
                local.append({"status": r["status"], "latency": r["latency"],
                              "completion": r["usage"].get("completion") or 0,
                              "prompt": r["usage"].get("prompt") or 0,
                              "error": (r["error"] or "")[:100]})
            with lock:
                stats.extend(local)

        t0 = time.perf_counter()
        with ThreadPoolExecutor(max_workers=conc) as ex:
            for _ in range(conc):
                ex.submit(worker)
        elapsed = time.perf_counter() - t0

        ok = [s for s in stats if s["status"] == 200]
        e429 = [s for s in stats if s["status"] == 429]
        other_err = [s for s in stats if s["status"] not in (200, 429)]
        lats = [s["latency"] for s in ok]
        rows.append({
            "concurrency": conc,
            "requests": len(stats),
            "rps": round(len(stats) / elapsed, 2) if elapsed else 0,
            "success": len(ok),
            "success_rate": round(len(ok) / len(stats), 3) if stats else 0,
            "err_429": len(e429),
            "err_other": len(other_err),
            "lat_p50": round(pct(lats, 50) or 0, 3),
            "lat_p95": round(pct(lats, 95) or 0, 3),
            "lat_p99": round(pct(lats, 99) or 0, 3),
            "ok_tokens_per_sec": round(
                sum(s["completion"] for s in ok) / elapsed, 1) if elapsed else 0,
            "sample_errors": list({s["error"] for s in other_err if s["error"]})[:3],
        })
    c = Case("A6-01", "A6 并发", "并发阶梯压测", "P95 升 1.5 倍或 429>1% 的较早者，再打 8 折")
    c.ok({"rows": rows, "seconds_per_level": ctx["load_seconds"]})
    out.append(c)
    return out


def suite_tpm(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    out: list[Case] = []
    model = models[0]
    prompt = "TPM 探测请求：请输出 200 字左右的说明。" * 3
    extra = {"max_tokens": 200, "temperature": 0}
    window = ctx["tpm_window"]

    rows = []
    rate = ctx["tpm_start"]
    while rate <= ctx["tpm_max"]:
        interval = window / rate
        # 必须并发提交：推理模型单请求 5–10 秒，串行的话实际速率被 1/延迟 卡死，
        # 永远到不了目标 TPM，测出来的"临界限"是探针自己的瓶颈。
        workers = max(1, min(64, rate))
        t_start = time.perf_counter()
        results: list[dict] = []
        lock = threading.Lock()

        def one() -> None:
            r = u.chat(model, [{"role": "user", "content": prompt}], extra=extra)
            with lock:
                results.append({"status": r["status"],
                                "tokens": (r["usage"].get("prompt") or 0)
                                          + (r["usage"].get("completion") or 0)})

        with ThreadPoolExecutor(max_workers=workers) as ex:
            futures = []
            for i in range(rate):
                target = t_start + i * interval
                now = time.perf_counter()
                if target > now:
                    time.sleep(target - now)
                futures.append(ex.submit(one))
            for f in futures:
                f.result()

        accepted_tokens = sum(r["tokens"] for r in results if r["status"] == 200)
        hits_429 = sum(1 for r in results if r["status"] == 429)
        rows.append({"requests_per_window": rate, "window_seconds": window,
                     "sent": len(results), "accepted_tokens": accepted_tokens,
                     "effective_tpm": round(accepted_tokens * 60 / window),
                     "hits_429": hits_429})
        if hits_429:
            break
        rate = int(rate * 1.2) + 1

    limit = rows[-1]["effective_tpm"] if rows else 0
    c = Case("A7-01", "A7 TPM", "TPM 速率阶梯", "出现 429 的档位即临界限；建议配额取 0.8 倍")
    (c.ok if rows else c.fail)(
        {"rows": rows, "suggested_quota": int(limit * 0.8),
         "note": "窗口越短越容易被突发桶掩盖，正式测请用 60s"})
    out.append(c)
    return out


def suite_responses(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    """Responses 协议（/v1/responses）。与 chat 是两套协议，口径要分开记。"""
    out: list[Case] = []
    model = models[0]
    big = {"max_output_tokens": 2048}

    # A10-01 非流式
    c = Case("A10-01", "A10 Responses 协议", "非流式调用",
             "200 且 status=completed，有 output_text，usage 齐全")
    r = u.responses(model, "只回复两个字：收到", extra=big)
    ok = (r["status"] == 200 and r["status_field"] == "completed"
          and bool(r["text"]) and r["usage"].get("prompt") is not None)
    (c.ok if ok else c.fail)(
        {"status": r["status"], "status_field": r["status_field"],
         "output_types": r["output_types"], "text": r["text"][:100],
         "usage": r["usage"].get("raw"), "error": (r["error"] or "")[:300]})
    out.append(c)

    # A10-02 流式事件序列
    c = Case("A10-02", "A10 Responses 协议", "流式事件序列",
             "能收到 response.completed，且事件序列可解析")
    r = u.responses(model, "数到五，用空格分隔", extra=big, stream=True)
    joined = " ".join(r["events"])
    has_completed = "response.completed" in joined or "response.incomplete" in joined
    (c.ok if r["status"] == 200 and has_completed and r["usage"].get("prompt") is not None else c.fail)(
        {"status": r["status"], "event_count": len(r["events"]),
         "first_events": r["events"][:6], "text": r["text"][:80],
         "usage": r["usage"].get("raw"), "error": (r["error"] or "")[:300]})
    out.append(c)

    # A10-03 与 chat 的 token 口径差
    c = Case("A10-03", "A10 Responses 协议", "与 chat 的输入 token 口径",
             "同一文本两条协议的输入 token 差应恒定（固定前导）或为 0")
    texts = ["只回复两个字：收到",
             "这是一段用于测试 token 计数一致性的中等长度文本，包含中文、English 和数字 12345。",
             "模型服务接入测试需要覆盖协议兼容性、计费口径、并发容量和故障语义四个方面。" * 5]
    rows = []
    for t in texts:
        rc = u.chat(model, [{"role": "user", "content": t}],
                    extra={"max_tokens": 8, "temperature": 0}, keep_budget=True)
        rr = u.responses(model, t, extra={"max_output_tokens": 256})
        cp = rc["usage"].get("prompt")
        rp = rr["usage"].get("prompt")
        rows.append({"chars": len(t), "chat_prompt_tokens": cp,
                     "responses_input_tokens": rp,
                     "delta": (rp - cp) if (cp is not None and rp is not None) else None})
    deltas = [x["delta"] for x in rows if x["delta"] is not None]
    constant = len(set(deltas)) == 1 if deltas else False
    (c.ok if deltas else c.fail)(
        {"rows": rows, "constant_delta": constant,
         "verdict": ("两条协议差固定前导 %+d token" % deltas[0]) if constant
                    else "差值不恒定，需按协议分别建账"},
         note="" if constant else "两条协议的输入计数不可互换")
    out.append(c)

    # A10-04 function tools
    c = Case("A10-04", "A10 Responses 协议", "function tools",
             "返回 function_call 且 arguments 是合法 JSON")
    tools = [{"type": "function", "name": "get_weather",
              "description": "查询指定城市的当前天气",
              "parameters": {"type": "object",
                             "properties": {"city": {"type": "string"}},
                             "required": ["city"]}}]
    r = u.responses(model, "北京现在天气怎么样？请调用工具查询。",
                    extra={**big, "tools": tools, "tool_choice": "required"})
    calls = [i for i in (r["raw"] or {}).get("output", []) if i.get("type") == "function_call"] \
        if r["raw"] else []
    name_ok = any(i.get("name") == "get_weather" for i in calls)
    args_ok = False
    for i in calls:
        try:
            json.loads(i.get("arguments") or "")
            args_ok = True
        except ValueError:
            pass
    (c.ok if r["status"] == 200 and name_ok and args_ok else c.fail)(
        {"status": r["status"], "function_calls": calls[:2],
         "text": r["text"][:100], "error": (r["error"] or "")[:300]})
    out.append(c)

    # A10-05 reasoning summary
    c = Case("A10-05", "A10 Responses 协议", "reasoning 输出项",
             "记录 output 里是否有 reasoning 项及其 summary")
    r = u.responses(model, "13 乘以 17 等于多少？只输出数字。", extra=big)
    has_reason = "reasoning" in (r["output_types"] or [])
    (c.ok)({"output_types": r["output_types"],
            "reasoning_summary_len": len(r["reasoning_summary"]),
            "text": r["text"][:60],
            "reasoning_tokens": r["usage"].get("reasoning")},
           note="有 reasoning 项说明思维链可被客户端读到，注意是否计费")
    out.append(c)
    return out


def gen_filler(chars: int, seed: int) -> str:
    """生成唯一随机填充文本。

    每次内容都不同，才能保证是「冷请求」：供应商的前缀缓存按字节匹配，
    复用同一段文本会让第二发直接命中，测出来的不是预填充耗时。
    """
    words = ("架构 推理 缓存 带宽 调度 集群 延迟 吞吐 显存 张量 量化 蒸馏 采样 对齐 "
             "检索 向量 训练 评估 基线 增量 熔断 降级 灰度 回滚 观测 埋点").split()
    r = random.Random(seed)
    out: list[str] = []
    n = 0
    while n < chars:
        s = " ".join(r.choice(words) for _ in range(12)) + f" 序{r.randint(10**6, 10**7 - 1)}。"
        out.append(s)
        n += len(s)
    return "".join(out)[:chars]


def _ctx_request(u: "Upstream", model: str, prefix: str, tail: str) -> dict:
    """发一条流式请求，只取 TTFT / 总耗时 / token 与缓存命中。"""
    r = u.chat(model, [{"role": "user", "content": prefix + tail}],
               stream=True, extra={"max_tokens": 32, "temperature": 0}, keep_budget=True)
    uu = r["usage"]
    pt = uu.get("prompt") or 0
    cached = uu.get("cache_read") or 0
    return {
        "status": r["status"],
        "prompt_tokens": pt,
        "cached_tokens": cached,
        "hit_pct": round(cached / pt * 100, 1) if pt else 0,
        "ttft_s": round(r["ttft"], 2) if r["ttft"] else None,
        "total_s": round(r["latency"], 2),
        "error": (r["error"] or "")[:200],
    }


def suite_context(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    """大上下文：冷请求阶梯、上限探测、缓存命中、前缀敏感性。"""
    out: list[Case] = []
    model = models[0]
    ratio = ctx.get("chars_per_token", 1.086)
    sizes = ctx["cold_ladder"]
    tail_ok = "\n\n请只回复 OK。"
    tail_done = "\n\n请只回复 DONE。"

    # A12-01 冷请求阶梯
    c = Case("A12-01", "A12 大上下文", f"冷请求阶梯（{sizes}）",
             "每档 cached_tokens 必须为 0；记录 TTFT 与预填充速率，应随长度近线性")
    rows = []
    for i, size in enumerate(sizes):
        text = gen_filler(int(size * ratio), 5000 + i)
        r = _ctx_request(u, model, text, tail_ok)
        rows.append({"target": size, **r})
        print(f"    A12-01 {size:>9,} -> prompt={r['prompt_tokens']:,} "
              f"ttft={r['ttft_s']}s cached={r['cached_tokens']:,}", flush=True)
        if r["status"] != 200:
            break
    ok = [x for x in rows if x["status"] == 200 and x["cached_tokens"] == 0]
    (c.ok if ok else c.fail)({"rows": rows},
        note="" if ok else "没有拿到干净冷请求（可能命中缓存或全部失败）")
    out.append(c)

    # A12-02 上下文上限探测（贵，默认跳过）
    if ctx.get("limit_probe"):
        c = Case("A12-02", "A12 大上下文", "上下文上限探测",
                 "逐档加压至 400，记录最大可接受 token 与错误信息")
        rows = []
        for i, size in enumerate(ctx["limit_probe"]):
            text = gen_filler(int(size * ratio), 6000 + i)
            r = _ctx_request(u, model, text, tail_ok)
            rows.append({"target": size, **r})
            print(f"    A12-02 {size:>9,} -> http={r['status']} "
                  f"prompt={r['prompt_tokens']:,}", flush=True)
            if r["status"] != 200:
                break
        c.ok({"rows": rows},
             note="超限报错是否给出明确原因需人工判读（看 error 字段）")
        out.append(c)
    else:
        out.append(Case("A12-02", "A12 大上下文", "上下文上限探测",
                        "逐档加压至 400，记录最大可接受 token 与错误信息")
                   .skip("默认跳过（单次 1M 级请求成本高）；加 --limit-probe 开启"))

    # A13-01 缓存命中
    c = Case("A13-01", "A13 缓存", "冷 vs 命中缓存",
             "同前缀重发的命中率应接近 100%，TTFT 显著下降")
    rows = []
    for i, size in enumerate(sizes):
        text = gen_filler(int(size * ratio), 5000 + i)   # 与 A12-01 同前缀
        cold = _ctx_request(u, model, text, tail_ok)
        time.sleep(3)
        warm_tail = _ctx_request(u, model, text, tail_done)
        time.sleep(1)
        warm_same = _ctx_request(u, model, text, tail_done)
        rows.append({"target": size, "cold": cold, "warm_tail": warm_tail, "warm_same": warm_same})
        print(f"    A13-01 {size:>9,} cold={cold['ttft_s']}s "
              f"warm={warm_tail['ttft_s']}s hit={warm_tail['hit_pct']}%", flush=True)
    good = [r for r in rows if r["warm_tail"]["hit_pct"] >= 90]
    ctx["_cache_works"] = bool(good)
    (c.ok if good else c.fail)({"rows": rows},
        note="" if good else "重发未获得高命中率，缓存可能未生效或按整条消息匹配")
    out.append(c)

    # A13-02 前缀敏感性
    # 供应商根本没有前缀缓存时，这条会「命中率为 0」而假通过，所以要依赖 A13-01 的结论。
    if sizes and ctx.get("_cache_works"):
        size = sizes[-1]
        chars = int(size * ratio)
        text = gen_filler(chars, 5000 + len(sizes) - 1)
        head = gen_filler(int(chars * 0.2), 9000)
        modified = head + text[int(chars * 0.2):]
        time.sleep(1)
        r = _ctx_request(u, model, modified, tail_ok)
        c = Case("A13-02", "A13 缓存", "前缀敏感性（只改开头 20%）",
                 "命中率应回落到 0，TTFT 回到冷请求水平")
        (c.ok if r["hit_pct"] == 0 else c.fail)(
            {"target": size, **r},
            note="" if r["hit_pct"] == 0 else "改前缀仍命中，缓存不是按前缀匹配")
        out.append(c)
    elif sizes:
        out.append(Case("A13-02", "A13 缓存", "前缀敏感性（只改开头 20%）",
                        "命中率应回落到 0，TTFT 回到冷请求水平")
                   .skip("A13-01 未观察到缓存命中，本条无信息量"))
    return out


# --------------------------------------------------------------------------
# 能力评估（quality）
# --------------------------------------------------------------------------

QUALITY_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "quality_items.json")

UNCERTAIN_MARKERS = ("不存在", "没有", "未", "无法", "不知道", "并无", "虚构", "不确定",
                     "无记载", "查无", "不可能", "尚未", "并不", "不是真实", "没有这部",
                     "从未", "未获", "无此", "不存在该")


def _norm(s: str) -> str:
    return (s or "").strip()


def score_item(check: dict, text: str) -> tuple[bool, str]:
    """返回 (是否通过, 判读说明)。"""
    t = _norm(text)
    kind = check.get("type")
    if kind == "regex":
        pat = check["pattern"]
        flags = re.I if check.get("flags") == "i" else 0
        if re.fullmatch(pat, t, flags):
            return True, "整串匹配"
        return False, f"未匹配 {pat}"
    if kind == "any":
        for pat in check["patterns"]:
            if re.search(pat, t, re.I):
                return True, f"命中 {pat}"
        return False, "未命中任一可接受答案"
    if kind == "num":
        nums = re.findall(r"-?\d+(?:\.\d+)?", t.replace(",", ""))
        if not nums:
            return False, "输出里没有数字"
        got = float(nums[-1])
        want = float(check["value"])
        tol = float(check.get("tol", 0))
        if abs(got - want) <= tol:
            return True, f"命中 {got:g}"
        return False, f"得到 {got:g}，期望 {want:g}"
    if kind == "forbid":
        bad = [w for w in check["words"] if w in t]
        return (not bad), ("出现禁用词 " + ",".join(bad)) if bad else "无禁用词"
    if kind == "uncertainty":
        hit = [m for m in UNCERTAIN_MARKERS if m in t]
        return bool(hit), ("指出不存在/未知" if hit else "未指出问题，疑似编造")
    return False, f"未知判据 {kind}"


def suite_quality(u: Upstream, models: list[str], ctx: dict) -> list[Case]:
    """智能水平评估：49 道自动判分题，覆盖 6 个维度。"""
    out: list[Case] = []
    model = models[0]
    if not os.path.exists(QUALITY_FILE):
        c = Case("A14-01", "A14 能力评估", "题库加载", "quality_items.json 存在")
        return [c.error(f"找不到题库 {QUALITY_FILE}")]
    bank = json.load(open(QUALITY_FILE, encoding="utf-8"))
    only = ctx.get("quality_dims")
    if only:
        bank = [i for i in bank if i["dim"] in only]

    results = []
    for it in bank:
        r = u.chat(model, [{"role": "user", "content": it["prompt"]}],
                   extra={"max_tokens": 8192, "temperature": 0}, keep_budget=True)
        ok, why = (False, (r["error"] or "请求失败")[:120])
        truncated = r.get("finish_reason") == "length"
        if r["status"] == 200:
            ok, why = score_item(it["check"], r["text"])
            if truncated:
                # 推理吃光预算时正文会被截断，判失败但原因要写清楚，
                # 否则会把「预算不够」误读成「模型不会」。
                ok, why = False, "输出被截断（finish_reason=length），非能力问题"
        results.append({"id": it["id"], "dim": it["dim"], "prompt": it["prompt"],
                        "status": r["status"], "output": (r["text"] or "")[:300],
                        "finish_reason": r.get("finish_reason"), "truncated": truncated,
                        "pass": ok, "why": why, "note": it.get("note", "")})
        print(f"    {it['id']:10s} {'PASS' if ok else 'FAIL'}  {why}", flush=True)

    dims: dict[str, list] = {}
    for r in results:
        dims.setdefault(r["dim"], []).append(r)
    names = {"IF": "指令遵循", "MATH": "数学", "LOGIC": "逻辑推理",
             "CODE": "代码理解", "ZH": "中文语言", "HALL": "抗幻觉", "HARD": "难题" }
    rows, total_pass = [], 0
    for dim, rs in dims.items():
        p = sum(1 for r in rs if r["pass"])
        total_pass += p
        rows.append({"dim": dim, "name": names.get(dim, dim),
                     "pass": p, "total": len(rs), "rate": round(p / len(rs), 3)})
    rows.sort(key=lambda x: x["dim"])

    c = Case("A14-01", "A14 能力评估", f"智能水平（{len(bank)} 题）",
             "按维度统计通过率；失败项需人工复核是否题目歧义")
    (c.ok if total_pass == len(bank) else c.ok)(
        {"dims": rows, "total_pass": total_pass, "total": len(bank),
         "overall": round(total_pass / len(bank), 3),
         "failures": [r for r in results if not r["pass"]]})
    out.append(c)
    return out


SUITES = {
    "smoke": suite_smoke,
    "fingerprint": suite_fingerprint,
    "tools": suite_tools,
    "params": suite_params,
    "limits": suite_limits,
    "load": suite_load,
    "tpm": suite_tpm,
    "responses": suite_responses,
    "context": suite_context,
    "quality": suite_quality,
}


# --------------------------------------------------------------------------
# 报告
# --------------------------------------------------------------------------

STATUS_MARK = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP", "error": "ERROR"}


def render_report(cases: list[Case], meta: dict) -> str:
    lines: list[str] = []
    lines.append("# 上游直连测试报告（Phase A）")
    lines.append("")
    lines.append(f"- 目标：`{meta['base_url']}`")
    lines.append(f"- 模型：{', '.join(f'`{m}`' for m in meta['models'])}")
    lines.append(f"- Key：`{mask_key(meta['api_key'])}`")
    lines.append(f"- 时间：{meta['started_at']} → {meta['finished_at']}")
    lines.append(f"- 套件：{', '.join(meta['suites'])}")
    lines.append("")

    total = len(cases)
    by_status: dict[str, int] = {}
    for c in cases:
        by_status[c.status] = by_status.get(c.status, 0) + 1
    lines.append("## 总览")
    lines.append("")
    lines.append("| 项目 | 数量 |")
    lines.append("|------|------|")
    lines.append(f"| 用例总数 | {total} |")
    for k in ("pass", "fail", "skip", "error"):
        if by_status.get(k):
            lines.append(f"| {STATUS_MARK[k]} | {by_status[k]} |")
    lines.append("")

    fails = [c for c in cases if c.status in ("fail", "error")]
    lines.append("## 缺陷与阻塞项")
    lines.append("")
    if fails:
        lines.append("| 编号 | 用例 | 状态 | 说明 |")
        lines.append("|------|------|------|------|")
        for c in fails:
            note = (c.note or _brief(c.data))[:160].replace("|", "\\|")
            lines.append(f"| {c.id} | {c.title} | {STATUS_MARK[c.status]} | {note} |")
    else:
        lines.append("无。")
    lines.append("")

    # 分组渲染
    groups: dict[str, list[Case]] = {}
    for c in cases:
        groups.setdefault(c.suite, []).append(c)
    for suite, items in groups.items():
        lines.append(f"## {suite}")
        lines.append("")
        lines.append("| 编号 | 用例 | 判据 | 结果 | 数据摘要 |")
        lines.append("|------|------|------|------|----------|")
        for c in items:
            summary = c.note or _brief(c.data)
            summary = summary[:200].replace("|", "\\|").replace("\n", " ")
            lines.append(f"| {c.id} | {c.title} | {c.judge} | {STATUS_MARK[c.status]} | {summary} |")
        lines.append("")

    # 关键指标
    lines.append("## 关键指标")
    lines.append("")
    for c in cases:
        if c.id in ("A2-04", "A6-01", "A7-01"):
            lines.append(f"### {c.id} {c.title}")
            lines.append("")
            if c.id == "A2-04":
                d = c.data
                lines.append(f"- 延迟 P50/P95：{d.get('latency_p50')}s / {d.get('latency_p95')}s")
                lines.append(f"- TTFT P50/P95：{d.get('ttft_p50_ms')}ms / {d.get('ttft_p95_ms')}ms")
                lines.append(f"- 输出速度 P50：{d.get('tokens_per_sec_p50')} tokens/s")
                lines.append(f"- 输出长度 P50：{d.get('completion_tokens_p50')} tokens")
            elif c.id == "A6-01":
                lines.append("| 并发 | RPS | 成功率 | 429 | 其他错误 | P50 | P95 | P99 | tokens/s |")
                lines.append("|------|-----|--------|-----|----------|-----|-----|-----|----------|")
                for r in c.data.get("rows", []):
                    lines.append(f"| {r['concurrency']} | {r['rps']} | {r['success_rate']} | "
                                 f"{r['err_429']} | {r['err_other']} | {r['lat_p50']} | "
                                 f"{r['lat_p95']} | {r['lat_p99']} | {r['ok_tokens_per_sec']} |")
            elif c.id == "A7-01":
                lines.append("| 请求/窗口 | 窗口(s) | 已发 | 计入 tokens | 有效 TPM | 429 |")
                lines.append("|-----------|---------|------|-------------|----------|-----|")
                for r in c.data.get("rows", []):
                    lines.append(f"| {r['requests_per_window']} | {r['window_seconds']} | "
                                 f"{r['sent']} | {r['accepted_tokens']} | "
                                 f"{r['effective_tpm']} | {r['hits_429']} |")
                lines.append("")
                lines.append(f"建议配额（0.8×）：**{c.data.get('suggested_quota')} TPM**")
            lines.append("")
    return "\n".join(lines)


def _brief(data: dict) -> str:
    if not data:
        return ""
    parts = []
    for k, v in data.items():
        if isinstance(v, (dict, list)):
            parts.append(f"{k}=<{type(v).__name__} len={len(v)}>")
        else:
            parts.append(f"{k}={v}")
        if len(", ".join(parts)) > 150:
            break
    return ", ".join(parts)


# --------------------------------------------------------------------------
# 内置 mock（--self-test）
# --------------------------------------------------------------------------

def start_mock() -> tuple[ThreadingHTTPServer, str]:
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def _json(self, code, body):
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _sse(self, chunks):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for ch in chunks:
                self.wfile.write(f"data: {json.dumps(ch)}\n\n".encode())
                self.wfile.flush()
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()

        def do_GET(self):
            if self.path.endswith("/models"):
                return self._json(200, {"object": "list",
                                        "data": [{"id": "mock-model", "object": "model"}]})
            self._json(404, {"error": {"message": "not found"}})

        def do_POST(self):
            n = int(self.headers.get("Content-Length") or 0)
            body = json.loads(self.rfile.read(n) or b"{}")
            auth = self.headers.get("Authorization", "")
            if "invalid" in auth:
                return self._json(401, {"error": {"message": "invalid api key"}})
            model = body.get("model")
            if model and "no_such" in model:
                return self._json(404, {"error": {"message": "model not found"}})

            if self.path.endswith("/responses"):
                txt = "ok"
                outs = [{"type": "reasoning",
                         "summary": [{"type": "summary_text", "text": "mock reasoning"}]},
                        {"type": "message", "role": "assistant",
                         "content": [{"type": "output_text", "text": txt}]}]
                if body.get("tools"):
                    outs.append({"type": "function_call", "name": body["tools"][0].get("name"),
                                 "arguments": json.dumps({"city": "北京"}, ensure_ascii=False),
                                 "call_id": "call_mock"})
                usage = {"input_tokens": 30, "input_tokens_details": {"cached_tokens": 0,
                                                                      "cache_write_tokens": 0},
                         "output_tokens": 12, "output_tokens_details": {"reasoning_tokens": 8},
                         "total_tokens": 42}
                resp = {"id": "resp_mock", "object": "response", "status": "completed",
                        "model": model, "output": outs, "usage": usage}
                if body.get("stream"):
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    events = [
                        ("response.created", {"type": "response.created",
                                              "response": {**resp, "status": "queued", "output": []}}),
                        ("response.output_text.delta", {"type": "response.output_text.delta",
                                                        "delta": txt}),
                        ("response.reasoning_summary_text.delta",
                         {"type": "response.reasoning_summary_text.delta", "delta": "mock"}),
                        ("response.completed", {"type": "response.completed", "response": resp}),
                    ]
                    for name, payload in events:
                        self.wfile.write(f"event: {name}\n".encode())
                        self.wfile.write(f"data: {json.dumps(payload)}\n\n".encode())
                        self.wfile.flush()
                    return
                return self._json(200, resp)

            msgs = body.get("messages") or []
            prompt_chars = sum(len(str(m.get("content") or "")) for m in msgs)
            prompt_tokens = max(1, prompt_chars // 2)
            tools = body.get("tools") or []
            want_tool = bool(tools) and (
                body.get("tool_choice") not in (None, "none")
                or any(k in str(msgs) for k in ("天气", "订单", "邮件"))
            )
            max_tokens = body.get("max_tokens") or 64

            if want_tool:
                fn = tools[0]["function"]
                name = fn["name"]
                if name == "get_weather":
                    args = {"city": "北京", "unit": "celsius"}
                elif name == "search_orders":
                    args = {"status": "paid", "filters": {"min_amount": 100, "tags": ["vip"]}, "limit": 5}
                else:
                    args = {"to": "a@example.com", "subject": "test", "body": "长文本" * 500}
                tc = {"id": "call_mock", "type": "function",
                      "function": {"name": name, "arguments": json.dumps(args, ensure_ascii=False)}}
                if body.get("stream"):
                    return self._sse([
                        {"id": "mock", "model": model,
                         "choices": [{"index": 0, "delta": {"tool_calls": [
                             {"index": 0, "id": "call_mock", "type": "function",
                              "function": {"name": name, "arguments": json.dumps(args)[:10]}}]}}]},
                        {"id": "mock", "model": model,
                         "choices": [{"index": 0, "delta": {"tool_calls": [
                             {"index": 0, "function": {"arguments": json.dumps(args)[10:]}}]}}],
                         "usage": {"prompt_tokens": prompt_tokens,
                                   "completion_tokens": 20,
                                   "total_tokens": prompt_tokens + 20}},
                    ])
                return self._json(200, {
                    "id": "chatcmpl-mock", "object": "chat.completion",
                    "created": int(time.time()), "model": model,
                    "choices": [{"index": 0, "finish_reason": "tool_calls",
                                 "message": {"role": "assistant", "content": None,
                                             "tool_calls": [tc]}}],
                    "usage": {"prompt_tokens": prompt_tokens, "completion_tokens": 20,
                              "total_tokens": prompt_tokens + 20},
                })

            text = "ok"
            if body.get("response_format"):
                text = json.dumps({"name": "张三", "age": 30, "tags": ["a", "b"]},
                                  ensure_ascii=False)
            if body.get("logprobs"):
                pass
            completion = min(max_tokens, 8)
            if body.get("stream"):
                return self._sse([
                    {"id": "mock", "model": model, "choices": [{"index": 0, "delta": {"content": text}}]},
                    {"id": "mock", "model": model, "choices": [{"index": 0, "delta": {}}],
                     "usage": {"prompt_tokens": prompt_tokens,
                               "completion_tokens": completion,
                               "total_tokens": prompt_tokens + completion}},
                ])
            body_out = {
                "id": "chatcmpl-mock", "object": "chat.completion",
                "created": int(time.time()), "model": model,
                "choices": [{"index": 0, "finish_reason": "stop",
                             "message": {"role": "assistant", "content": text}}],
                "usage": {"prompt_tokens": prompt_tokens, "completion_tokens": completion,
                          "total_tokens": prompt_tokens + completion},
            }
            if body.get("n", 1) > 1:
                body_out["choices"] = [
                    {"index": i, "finish_reason": "stop",
                     "message": {"role": "assistant", "content": f"{text}{i}"}}
                    for i in range(body["n"])]
            if body.get("logprobs"):
                body_out["choices"][0]["logprobs"] = {"content": [
                    {"token": "ok", "logprob": -0.01}]}
            self._json(200, body_out)

    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_address[1]}/v1"


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def main() -> int:
    ap = argparse.ArgumentParser(description="上游供应商直连探针")
    ap.add_argument("--base-url", default=os.environ.get("UPSTREAM_BASE_URL", ""))
    ap.add_argument("--api-key", default=os.environ.get("UPSTREAM_API_KEY", ""))
    ap.add_argument("--model", action="append", default=[],
                    help="可重复；第一个用于绝大多数用例")
    ap.add_argument("--suites", default=None,
                    help=f"逗号分隔，可选：{','.join(SUITES)}")
    ap.add_argument("--out", default="")
    ap.add_argument("--timeout", type=int, default=120)
    ap.add_argument("--auth-header", default="Authorization")
    ap.add_argument("--auth-prefix", default="Bearer ")
    ap.add_argument("--max-tokens-field", default="max_tokens",
                    choices=["max_tokens", "max_completion_tokens"],
                    help="输出预算字段。推理模型常忽略 max_tokens，需改用 max_completion_tokens")
    ap.add_argument("--budget-floor", type=int, default=0,
                    help="给需要正文的用例抬预算地板，避免推理吃光配额导致 content 为空")
    ap.add_argument("--concurrency", default="1,5,10,20")
    ap.add_argument("--load-seconds", type=int, default=120)
    ap.add_argument("--context-ladder", default="8000,32000,64000")
    ap.add_argument("--cold-ladder", default="131072",
                    help="context 套件的冷请求档位（目标 token 数，逗号分隔）；"
                         "每档约 4 次请求，512K 以上单档就上百万 token")
    ap.add_argument("--limit-probe", default="",
                    help="上下文上限探测的候选 token 数，如 1055000,1100000；留空跳过")
    ap.add_argument("--temperature", type=float, default=None,
                    help="覆盖用例里的 temperature（部分模型只接受固定值，例如 Kimi K3 只允许 1）")
    ap.add_argument("--quality-dims", default="",
                    help="只跑指定维度（逗号分隔），如 HARD；留空跑全部")
    ap.add_argument("--chars-per-token", type=float, default=1.086,
                    help="生成填充文本时的字符/token 比，用于逼近目标档位")
    ap.add_argument("--tpm-window", type=int, default=60)
    ap.add_argument("--tpm-start", type=int, default=10)
    ap.add_argument("--tpm-max", type=int, default=200)
    ap.add_argument("--self-test", action="store_true", help="用内置 mock 验证探针本身")
    args = ap.parse_args()

    started = datetime.now().strftime("%Y-%m-%d %H:%M:%S")

    if args.self_test:
        srv, base = start_mock()
        args.base_url = base
        args.api_key = "sk-mock-key"
        args.model = ["mock-model"]
        if not args.suites:
            args.suites = "smoke,tools,params"
            args.context_ladder = "1000,4000"
        print(f"[self-test] mock 已启动：{base}")
    else:
        if not args.base_url or not args.api_key:
            ap.error("必须提供 --base-url 与 --api-key（或 UPSTREAM_BASE_URL / UPSTREAM_API_KEY）")
        if not args.model:
            ap.error("必须提供至少一个 --model")
        if not args.suites:
            args.suites = "smoke,fingerprint,tools,params,limits"

    suite_names = [s.strip() for s in args.suites.split(",") if s.strip()]
    unknown = [s for s in suite_names if s not in SUITES]
    if unknown:
        ap.error(f"未知套件：{unknown}")

    u = Upstream(args.base_url, args.api_key, timeout=args.timeout,
                 auth_header=args.auth_header, auth_prefix=args.auth_prefix,
                 maxtok_field=args.max_tokens_field, budget_floor=args.budget_floor,
                 temperature=args.temperature)
    resolved = u.resolve_base()
    print(f"实际 base：{resolved}")

    ctx = {
        "ladder": [int(x) for x in args.context_ladder.split(",") if x.strip()],
        "concurrency": [int(x) for x in args.concurrency.split(",") if x.strip()],
        "load_seconds": args.load_seconds,
        "tpm_window": args.tpm_window,
        "cold_ladder": [int(x) for x in args.cold_ladder.split(",") if x.strip()],
        "limit_probe": [int(x) for x in args.limit_probe.split(",") if x.strip()],
        "chars_per_token": args.chars_per_token,
        "quality_dims": [x.strip().upper() for x in args.quality_dims.split(",") if x.strip()],
        "tpm_start": args.tpm_start,
        "tpm_max": args.tpm_max,
    }

    cases: list[Case] = []
    for name in suite_names:
        print(f"== {name} ==")
        try:
            got = SUITES[name](u, args.model, ctx)
        except Exception as e:  # 任何套件崩溃都不该拖垮整轮
            c = Case(f"{name.upper()}-XX", name, f"{name} 套件异常", "套件应完整执行")
            c.error(f"{type(e).__name__}: {e}")
            got = [c]
        for c in got:
            print(f"  {c.id:8s} {STATUS_MARK[c.status]:5s} {c.title}"
                  + (f"  ({c.note})" if c.note else ""))
        cases.extend(got)

    finished = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    out_dir = args.out or os.path.join(
        os.path.dirname(os.path.abspath(__file__)),
        "results", datetime.now().strftime("%Y%m%d-%H%M%S"))
    os.makedirs(out_dir, exist_ok=True)

    meta = {
        "base_url": args.base_url,
        "resolved_base": resolved,
        "models": args.model,
        "api_key": args.api_key,
        "started_at": started,
        "finished_at": finished,
        "suites": suite_names,
    }
    with open(os.path.join(out_dir, "cases.json"), "w", encoding="utf-8") as f:
        json.dump([c.to_dict() for c in cases], f, ensure_ascii=False, indent=2)
    with open(os.path.join(out_dir, "raw.json"), "w", encoding="utf-8") as f:
        json.dump({"meta": {**meta, "api_key": mask_key(args.api_key)},
                   "cases": [c.to_dict() for c in cases]},
                  f, ensure_ascii=False, indent=2)
    report = render_report(cases, meta)
    with open(os.path.join(out_dir, "report.md"), "w", encoding="utf-8") as f:
        f.write(report)

    print(f"\n报告：{os.path.join(out_dir, 'report.md')}")
    fail_n = sum(1 for c in cases if c.status in ("fail", "error"))
    print(f"用例 {len(cases)} 个，失败/异常 {fail_n} 个")
    if args.self_test:
        srv.shutdown()
    return 1 if fail_n else 0


if __name__ == "__main__":
    sys.exit(main())
