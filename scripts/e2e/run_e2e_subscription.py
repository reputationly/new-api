"""套餐与算力点的端到端用例（S）。由 run_e2e.py 在启动时导入并按顺序追加。

依赖 run_e2e.py 作为 __main__ 运行：用例注册表、CTX 与工具函数都取自那里。
"""
import json
import time

from __main__ import (  # noqa: E402 —— 由 run_e2e.py 以脚本方式运行时导入
    BASE, CTX, QPCP, QPU, case, epay_notify, epay_pay, eq, evidence, exec_sql, expect,
    fund_entries, fund_op, last_log, make_user, q, q1, set_option, user_row, assert_consistent,
    session_proxies,
)
import requests


# ---------------------------------------------------------------------------
# 工具
# ---------------------------------------------------------------------------

def ent(models, **kw):
    e = {"models": models, "consume_points": True, "consume_discount": 1, "limit_count": 0,
         "reset_period": "monthly", "rate_limit_rpm": 60, "channel_ids": ""}
    e.update(kw)
    return e


def create_plan(title, price, total_amount=0, points=0, entitlements=None):
    plan = {"title": title, "price_amount": price, "currency": "CNY", "total_amount": total_amount,
            "duration_unit": "month", "duration_value": 1, "enabled": True,
            "quota_reset_period": "monthly", "compute_points_per_period": points * QPCP}
    res = CTX["root"].post("/api/subscription/admin/plans", {"plan": plan, "entitlements": entitlements or []})
    return res


def plan_id_by_title(title):
    return q1("SELECT id FROM subscription_plans WHERE title=?", title)["id"]


def buy(client, plan_id):
    """走真实购买：订阅下单 + 自签易支付回调。"""
    params = epay_pay(client, "/api/subscription/epay/pay", {"plan_id": plan_id, "payment_method": "alipay"})
    eq(epay_notify("/api/subscription/epay/notify", params["out_trade_no"], params["money"]), "success", "订阅回调")
    sub = q1("SELECT * FROM user_subscriptions WHERE user_id=? AND plan_id=? ORDER BY id DESC", client.uid, plan_id)
    expect(sub, "购买后没有生成订阅")
    return params["out_trade_no"], sub


def lot_of(sub_id):
    return q1("SELECT * FROM compute_point_lots WHERE ref_id=? AND source='subscription' ORDER BY id DESC", sub_id)


def counter(sub_id, models):
    return q1("""SELECT c.* FROM user_subscription_entitlements c
                 JOIN subscription_plan_entitlements e ON e.id = c.plan_entitlement_id
                 WHERE c.user_subscription_id=? AND e.models=?""", sub_id, models)


def set_channel_status(ch_id, status):
    CTX["root"].ok("PUT", "/api/channel/", {"id": ch_id, "status": status})


# ---------------------------------------------------------------------------
# 4.1 配置与购买
# ---------------------------------------------------------------------------

@case("S0", "准备：关闭积分（降级扣费应走纯钱包）、给 dave 充值")
def s0():
    set_option("points_setting.enabled", False)
    res = fund_op(CTX["dave"].uid, op="prepay", value=5 * QPU, cash_fen=3650, ref="R-dave", remark="e2e")
    expect(res.get("success"), f"dave 入账失败：{res}")


@case("S1", "换算率下发")
def s1():
    st = requests.get(BASE + "/api/status", timeout=10, proxies=session_proxies).json()["data"]
    eq(st.get("quota_per_compute_point"), QPCP, "status.quota_per_compute_point")


@case("S2", "权益校验：不合规配置被拒")
def s2():
    bad = [
        ("不限次且没填 RPM", ent("m1", rate_limit_rpm=0)),
        ("不消耗算力点且没填 RPM", ent("m1", consume_points=False, limit_count=10, rate_limit_rpm=0)),
    ]
    for what, e in bad:
        res = create_plan(f"bad-{what}", 1, points=10, entitlements=[e])
        expect(not res.get("success"), f"{what} 应被拒：{res}")
    # 折扣 0 与「没填」在 JSON 里无法区分，按默认 1 落库（编辑页在前端就拦了 0）
    res = create_plan("discount-zero", 1, points=10, entitlements=[ent("m-zero", consume_discount=0, limit_count=10)])
    expect(res.get("success"), f"折扣 0 应按默认值保存：{res}")
    eq(q1("SELECT consume_discount FROM subscription_plan_entitlements WHERE models='m-zero'")["consume_discount"], 1, "落库折扣")


@case("S3", "最坏成本预览")
def s3():
    def preview(channel):
        return CTX["root"].ok("POST", "/api/subscription/admin/entitlement_cost_preview", {
            "models": ["e2e-image", "e2e-chat"], "channel_ids": [str(channel)], "limit_count": 3,
            "reset_period": "monthly", "duration_unit": "month", "duration_value": 1})
    # 渠道 A 只给 e2e-chat 配了成本：按次模型也必须如实说「无法估算」，不能当成 0
    d = preview(CTX["ch_mock-a"])
    img = next(m for m in d["models"] if m["model"] == "e2e-image")
    expect(not img["resolvable"] and "未配置" in img.get("reason", ""), f"未配成本应标无法估算：{img}")
    # 渠道 B 给 e2e-image 配成本 0.6（B 不承载它，只供试算）：单次 20000×0.6，3 次
    CTX["root"].ok("PUT", f"/api/channel/{CTX['ch_mock-b']}/cost",
                   {"costs": [{"model_name": "e2e-image", "cost_ratio": 0.6, "remark": "e2e"}]})
    d = preview(CTX["ch_mock-b"])
    evidence("S3", "preview", d)
    img = next(m for m in d["models"] if m["model"] == "e2e-image")
    chat = next(m for m in d["models"] if m["model"] == "e2e-chat")
    expect(img["resolvable"], f"配了成本的按次模型应能换算：{img}")
    eq(img["unit_quota"], 12000, "单次成本")
    eq(d["worst_total_quota"], 36000, "3 次最坏成本")
    expect(not chat["resolvable"], "按量模型应标无法换算")
    return f"worst_total_quota={d['worst_total_quota']}"


@case("S4", "建套餐 + 真实购买 P1")
def s4():
    ch_b = str(CTX["ch_mock-b"])
    for title, price, total, points, ents in [
        ("专业版", 29.9, 0, 1000, [
            ent("e2e-chat,e2e-fail"),
            ent("e2e-image", consume_discount=0.5, limit_count=3),
            ent("e2e-limited", channel_ids=ch_b),
            ent("deepseek-v4-flash-0731"),
        ]),
        ("老式套餐", 9.9, QPU * 2, 0, []),
        ("限速套餐", 1, 0, 1000, [ent("e2e-chat", rate_limit_rpm=2)]),
    ]:
        res = create_plan(title, price, total, points, ents)
        expect(res.get("success"), f"建套餐 {title} 失败：{res}")
        CTX[f"plan_{title}"] = plan_id_by_title(title)

    alice = CTX["alice"]
    quota_before = user_row(alice.uid)["quota"]
    trade, sub = buy(alice, CTX["plan_专业版"])
    CTX["alice_sub"] = sub["id"]
    rows = fund_entries(ref_type="subscription_order", ref_id=trade)
    evidence("S4", "fund", rows)
    eq(len(rows), 1, "收入流水条数")
    eq((rows[0]["account"], rows[0]["kind"], rows[0]["source"], rows[0]["cash_fen"]),
       ("subscription", "prepay", "subscription", 2990), "收入流水")
    lot = lot_of(sub["id"])
    eq(lot["points_total"], 1000 * QPCP, "批次 points_total")
    n = q1("SELECT COUNT(*) n FROM user_subscription_entitlements WHERE user_subscription_id=?", sub["id"])["n"]
    eq(n, 4, "权益计数器条数")
    eq(user_row(alice.uid)["quota"], quota_before, "users.quota 不变")


@case("S5", "老式额度：新式套餐为「无」")
def s5():
    plans = CTX["alice"].ok("GET", "/api/subscription/plans")
    flags = {p["plan"]["title"]: p.get("no_legacy_quota", False) for p in plans}
    eq(flags.get("专业版"), True, "专业版 no_legacy_quota")
    eq(flags.get("老式套餐"), False, "老式套餐 no_legacy_quota")
    me = CTX["alice"].ok("GET", "/api/subscription/self")
    active = me["subscriptions"][0]
    eq(active.get("no_legacy_quota"), True, "我的订阅 no_legacy_quota")


# ---------------------------------------------------------------------------
# 4.2 扣费链路（假上游，金额精确）
# ---------------------------------------------------------------------------

def call(client, model, **kw):
    code, body = client.relay(model, **kw)
    return code, body, last_log(client.uid)


@case("S6", "套餐内扣点（e2e-chat）")
def s6():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    used0, quota0 = lot_of(sub)["points_used"], user_row(alice.uid)["quota"]
    code, _, log = call(alice, "e2e-chat")
    eq(code, 200, "调用状态")
    o = log["other_json"]
    evidence("S6", "log", log)
    eq(o.get("billing_source"), "entitlement", "billing_source")
    eq(lot_of(sub)["points_used"] - used0, 15000, "批次扣减（quota）")
    eq(user_row(alice.uid)["quota"], quota0, "钱包不变")
    eq(o.get("compute_points"), 150, "日志 compute_points")
    eq(o.get("entitlement_plan_title"), "专业版", "日志套餐名")
    eq(o.get("cost_quota"), 7500, "日志 cost_quota（成本比 0.5）")


@case("S7", "折扣与次数（e2e-image ×3）")
def s7():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    used_counts = []
    for i in range(3):
        before = lot_of(sub)["points_used"]
        code, _, log = call(alice, "e2e-image")
        eq(code, 200, f"第 {i + 1} 次状态")
        eq(log["other_json"].get("billing_source"), "entitlement", f"第 {i + 1} 次 billing_source")
        eq(lot_of(sub)["points_used"] - before, 10000, f"第 {i + 1} 次扣点（20000×0.5）")
        used_counts.append(log["other_json"].get("entitlement_used_count"))
    eq(used_counts, [1, 2, 3], "日志里的已用次数")
    eq(counter(sub, "e2e-image")["used_count"], 3, "计数器 used_count")


@case("S8", "次数用尽 → 降级走钱包")
def s8():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    quota0, used0 = user_row(alice.uid)["quota"], lot_of(sub)["points_used"]
    code, _, log = call(alice, "e2e-image")
    eq(code, 200, "调用状态（降级不失败）")
    o = log["other_json"]
    evidence("S8", "log", log)
    eq(o.get("billing_source"), "wallet", "billing_source")
    fb = o.get("entitlement_fallback") or {}
    eq((fb.get("reason"), fb.get("limit_count")), ("count_exhausted", 3), "降级记录")
    eq(quota0 - user_row(alice.uid)["quota"], 20000, "钱包扣费")
    eq(lot_of(sub)["points_used"], used0, "点数不动")
    eq(counter(sub, "e2e-image")["used_count"], 3, "计数器不超上限")


@case("S9", "点数不足：结算扣光余量，下一次降级走钱包且不占次数")
def s9():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    lot = lot_of(sub)
    saved = lot["points_used"]
    try:
        exec_sql("UPDATE compute_point_lots SET points_used=? WHERE id=?", lot["points_total"] - 50 * QPCP, lot["id"])
        # 第一步：预扣只占估算的输入部分（< 50 点）能过，服务交付；结算补扣不足，
        # 把剩下的扣光、差额由平台承担——而不是「扣不到就原样放过」
        code, _, log = call(alice, "e2e-chat")
        eq(code, 200, "第一次调用状态")
        eq(log["other_json"].get("billing_source"), "entitlement", "第一次走权益")
        eq(lot_of(sub)["points_used"], lot["points_total"], "结算后余量被扣光")
        # 第二步：余量为 0，预扣失败，降级走钱包，且不占次数
        cnt0 = counter(sub, "e2e-chat,e2e-fail")["used_count"]
        code, _, log = call(alice, "e2e-chat")
        eq(code, 200, "第二次调用状态")
        o = log["other_json"]
        evidence("S9", "log", log)
        eq(o.get("billing_source"), "wallet", "第二次 billing_source")
        fb = o.get("entitlement_fallback") or {}
        eq(fb.get("reason"), "points_insufficient", "降级原因")
        expect(fb.get("points_needed", 0) > 0, f"应记下所需点数：{fb}")
        eq(fb.get("points_available", 0), 0, "剩余点数")
        eq(counter(sub, "e2e-chat,e2e-fail")["used_count"], cnt0, "计数器不增加")
        return f"points_needed={fb.get('points_needed')}"
    finally:
        exec_sql("UPDATE compute_point_lots SET points_used=? WHERE id=?", saved, lot["id"])


@case("S10", "套餐外模型走钱包（不是无限的老式额度）")
def s10():
    alice = CTX["alice"]
    quota0 = user_row(alice.uid)["quota"]
    code, _, log = call(alice, "e2e-outside")
    eq(code, 200, "调用状态")
    o = log["other_json"]
    eq(o.get("billing_source"), "wallet", "billing_source")
    expect("entitlement_fallback" not in o, f"套餐外不是降级：{o.get('entitlement_fallback')}")
    eq(quota0 - user_row(alice.uid)["quota"], 15000, "钱包扣费")


@case("S11", "失败退款：点数与次数原路退回")
def s11():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    used0 = lot_of(sub)["points_used"]
    cnt0 = counter(sub, "e2e-chat,e2e-fail")["used_count"]
    quota0 = user_row(alice.uid)["quota"]
    code, body = alice.relay("e2e-fail")
    expect(code != 200, f"e2e-fail 应失败：{code}")
    eq(lot_of(sub)["points_used"], used0, "点数退回")
    eq(counter(sub, "e2e-chat,e2e-fail")["used_count"], cnt0, "次数退回")
    eq(user_row(alice.uid)["quota"], quota0, "钱包不动")
    return f"status={code}"


@case("S12", "偏好「仅钱包」跳过权益")
def s12():
    alice = CTX["alice"]
    alice.ok("PUT", "/api/subscription/self/preference", {"billing_preference": "wallet_only"})
    try:
        code, _, log = call(alice, "e2e-chat")
        eq(code, 200, "调用状态")
        eq(log["other_json"].get("billing_source"), "wallet", "billing_source")
        expect("entitlement_fallback" not in log["other_json"], "仅钱包不是降级")
    finally:
        alice.ok("PUT", "/api/subscription/self/preference", {"billing_preference": "subscription_first"})


@case("S13", "限渠道权益")
def s13():
    alice = CTX["alice"]
    a, b = CTX["ch_mock-a"], CTX["ch_mock-b"]
    try:
        set_channel_status(a, 2)
        time.sleep(1)
        code, _, log = call(alice, "e2e-limited")
        eq(code, 200, "只留渠道 B 时调用状态")
        eq(log["other_json"].get("billing_source"), "entitlement", "落在渠道 B → 走权益")
        set_channel_status(a, 1)
        set_channel_status(b, 2)
        time.sleep(1)
        code, _, log = call(alice, "e2e-limited")
        eq(code, 200, "只留渠道 A 时调用状态")
        eq(log["other_json"].get("billing_source"), "wallet", "落在渠道 A → 走钱包")
        expect("entitlement_fallback" not in log["other_json"], "渠道不符是未命中，不是降级")
    finally:
        set_channel_status(a, 1)
        set_channel_status(b, 1)


@case("S14", "RPM 超限 → 降级走钱包，不占次数不扣点")
def s14():
    dave = CTX["dave"]
    _, sub = buy(dave, CTX["plan_限速套餐"])
    CTX["dave_sub"] = sub["id"]
    sources = []
    for _ in range(3):
        code, _, log = call(dave, "e2e-chat")
        eq(code, 200, "调用状态")
        sources.append(log["other_json"].get("billing_source"))
    eq(sources, ["entitlement", "entitlement", "wallet"], "前两次走权益、第三次降级")
    fb = log["other_json"].get("entitlement_fallback") or {}
    eq((fb.get("reason"), fb.get("rate_limit_rpm")), ("rate_limited", 2), "降级记录")
    eq(lot_of(sub["id"])["points_used"], 2 * 15000, "只扣了两次的点")
    eq(counter(sub["id"], "e2e-chat")["used_count"], 2, "只占了两次")


@case("S15", "老式套餐不受影响")
def s15():
    bob = CTX["bob"]
    _, sub = buy(bob, CTX["plan_老式套餐"])
    quota0 = user_row(bob.uid)["quota"]
    code, _, log = call(bob, "e2e-outside")
    eq(code, 200, "调用状态")
    eq(log["other_json"].get("billing_source"), "subscription", "billing_source")
    eq(user_row(bob.uid)["quota"], quota0, "钱包不变")
    used = q1("SELECT amount_used FROM user_subscriptions WHERE id=?", sub["id"])["amount_used"]
    eq(used, 15000, "订阅 amount_used")


@case("S16", "资金自洽（套餐消费不串进现金）")
def s16():
    assert_consistent("S16")


# ---------------------------------------------------------------------------
# 4.3 真实上游
# ---------------------------------------------------------------------------

def real_model():
    return "deepseek-v4-flash-0731"


@case("S17", "真实上游：非流式对话走权益")
def s17():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    used0 = lot_of(sub)["points_used"]
    code, body, log = call(alice, real_model(), max_tokens=16)
    evidence("S17", "log", log)
    eq(code, 200, f"调用状态 {body if code != 200 else ''}")
    o = log["other_json"]
    eq(o.get("billing_source"), "entitlement", "billing_source")
    spent = lot_of(sub)["points_used"] - used0
    eq(spent, log["quota"], "批次扣减 = 日志 quota（折扣 1）")
    eq(o.get("compute_points_quota"), log["quota"], "日志 compute_points_quota")
    eq(o.get("compute_points"), -(-log["quota"] // QPCP), "日志 compute_points = ceil(quota/100)")
    return f"quota={log['quota']} tokens={log['prompt_tokens']}+{log['completion_tokens']}"


@case("S18", "真实上游：流式对话结算一致")
def s18():
    alice, sub = CTX["alice"], CTX["alice_sub"]
    used0 = lot_of(sub)["points_used"]
    code, text, log = call(alice, real_model(), stream=True, max_tokens=16)
    eq(code, 200, "调用状态")
    expect("[DONE]" in text, f"流没有正常结束：{text[-200:]}")
    eq(log["other_json"].get("billing_source"), "entitlement", "billing_source")
    eq(lot_of(sub)["points_used"] - used0, log["quota"], "结算后批次扣减 = 日志 quota")
    return f"quota={log['quota']}"


# ---------------------------------------------------------------------------
# 4.4 展示（接口层；界面由 Playwright 覆盖）
# ---------------------------------------------------------------------------

@case("S19", "模型广场覆盖数据")
def s19():
    cov = CTX["alice"].ok("GET", "/api/pricing")
    raw = CTX["alice"].get("/api/pricing")
    coverage = raw.get("entitlement_coverage") or {}
    evidence("S19", "coverage", coverage)
    for m in ("e2e-chat", "e2e-image", "e2e-limited"):
        expect(m in coverage, f"alice 的覆盖里缺 {m}")
    eq(coverage["e2e-image"]["limit_count"], 3, "e2e-image 上限")
    eq(coverage["e2e-limited"]["channel_limited"], True, "e2e-limited 限渠道")
    eq(CTX["bob"].get("/api/pricing").get("entitlement_coverage"), None, "bob（只有老式套餐）")
    anon = requests.get(BASE + "/api/pricing", timeout=10, proxies=session_proxies).json()
    eq(anon.get("entitlement_coverage"), None, "匿名")
    _ = cov


@case("S21", "套餐对比表")
def s21():
    set_option("compute_point_setting.showcase_models", ["e2e-image", "e2e-chat"])
    d = CTX["alice"].ok("GET", "/api/subscription/plans/comparison")
    evidence("S21", "comparison", d)
    cols = {c["plan_id"]: c for c in d["plans"]}
    p1 = cols[CTX["plan_专业版"]]
    eq(p1["compute_points_per_period"], 1000 * QPCP, "P1 每期点数")
    eq(p1["coverage"]["e2e-image"]["discount"], 0.5, "P1 e2e-image 折扣")
    expect(any(l["models"] == "e2e-image" and l["limit_count"] == 3 for l in p1["limits"]), "P1 次数上限段")
    expect(CTX["plan_老式套餐"] not in cols, "老式套餐不应出列")
    eq([m["model_name"] for m in d["models"]], ["e2e-image", "e2e-chat"], "展示模型顺序")


@case("S22", "我的订阅余量")
def s22():
    me = CTX["alice"].ok("GET", "/api/subscription/self")
    evidence("S22", "self", me)
    s = next(x for x in me["subscriptions"] if x["subscription"]["plan_id"] == CTX["plan_专业版"])
    eq(s.get("plan_title"), "专业版", "plan_title")
    lot = lot_of(CTX["alice_sub"])
    eq(s["compute_points"]["available"], lot["points_total"] - lot["points_used"], "可用点数")
    img = next(e for e in s["entitlements"] if e["models"] == "e2e-image")
    eq((img["limit_count"], img["used_count"]), (3, 3), "e2e-image 次数")


@case("S24", "使用日志：计费来源筛选")
def s24():
    alice = CTX["alice"]
    def n(billing):
        d = alice.ok("GET", f"/api/log/self/?p=1&page_size=100&type=0&billing={billing}")
        return d["total"]
    ents = sum(1 for r in q("SELECT other FROM logs WHERE user_id=? AND type IN (2,6)", alice.uid)
               if json.loads(r["other"] or "{}").get("billing_source") == "entitlement")
    overs = sum(1 for r in q("SELECT other FROM logs WHERE user_id=? AND type IN (2,6)", alice.uid)
                if "entitlement_fallback" in json.loads(r["other"] or "{}"))
    eq(n("entitlement"), ents, "套餐内条数")
    eq(n("overage"), overs, "超额条数")
    res = CTX["root"].get("/api/log/?p=1&page_size=10&type=0&billing=overage")
    expect(not res.get("success"), f"管理端不带起始时间应被拒：{res}")
    res = CTX["root"].get(f"/api/log/?p=1&page_size=10&type=0&billing=overage&start_timestamp={int(time.time()) - 3600}")
    expect(res.get("success"), f"带起始时间应成功：{res}")
    return f"entitlement={ents} overage={overs}"


# ---------------------------------------------------------------------------
# 4.5 履约率报表
# ---------------------------------------------------------------------------

def fulfillment():
    now = int(time.time())
    return CTX["root"].ok("GET", f"/api/reconcile/admin/plan/fulfillment?start={now - 86400}&end={now + 60}")


def row_of(report, plan_id):
    return next((r for r in report["rows"] if r["plan_id"] == plan_id), None)


@case("S27", "履约率报表数字")
def s27():
    rep = fulfillment()
    evidence("S27", "report", rep)
    p1 = row_of(rep, CTX["plan_专业版"])
    expect(p1, "报表里没有专业版")
    eq((p1["order_count"], p1["revenue_fen"]), (1, 2990), "收入")
    # 外采成本：只有 e2e-chat 配了成本比；按日志逐条加总 cost_quota 折分
    cost_q = 0.0
    uncosted = 0
    for r in q("SELECT type, other FROM logs WHERE type IN (2,6)"):
        o = json.loads(r["other"] or "{}")
        if o.get("billing_source") != "entitlement" or o.get("entitlement_plan_id") != CTX["plan_专业版"]:
            continue
        sign = -1 if r["type"] == 6 else 1
        if "cost_quota" in o:
            cost_q += sign * o["cost_quota"]
        elif r["type"] == 2:
            uncosted += 1
    eq(p1["cost_fen"], round(cost_q / QPU * 7.3 * 100), "外采成本（分）")
    eq(p1["uncosted_call_count"], uncosted, "成本未知的调用数")
    eq(p1["overage_count"], 2, "超额次数（S8 次数用尽 + S9 点数不足）")
    p3 = row_of(rep, CTX["plan_限速套餐"])
    eq(p3["overage_count"], 1, "限速套餐超额（S14）")
    # 老式套餐的履约成本是扣老式额度的调用：S15 bob 调了一次，不能显示成 0 次
    p2 = row_of(rep, CTX["plan_老式套餐"])
    eq(p2["call_count"], 1, "老式套餐的套餐内调用（S15）")
    return f"P1 cost_fen={p1['cost_fen']} rate={p1['fulfillment_rate']}"


@case("S28", "过期作废比例")
def s28():
    lot = lot_of(CTX["alice_sub"])
    exec_sql("UPDATE compute_point_lots SET expires_at=? WHERE id=?", int(time.time()) - 10, lot["id"])
    p1 = row_of(fulfillment(), CTX["plan_专业版"])
    unused = lot["points_total"] - lot["points_used"]
    eq(p1["points_expired_granted"], lot["points_total"] // QPCP, "到期发放点数")
    eq(p1["points_expired_unused"], unused // QPCP, "作废点数")
    expect(abs(p1["expired_unused_ratio"] - unused / lot["points_total"]) < 1e-9, "作废比例")
    # 恢复，后面的界面用例还要用余量
    exec_sql("UPDATE compute_point_lots SET expires_at=? WHERE id=?", lot["expires_at"], lot["id"])


@case("S29", "管理员开通：有成本、无收入")
def s29():
    frank = CTX.get("frank")
    CTX["root"].ok("POST", "/api/subscription/admin/bind", {"user_id": frank.uid, "plan_id": CTX["plan_专业版"]})
    before = row_of(fulfillment(), CTX["plan_专业版"])
    code, _, log = call(frank, "e2e-chat")
    eq(code, 200, "调用状态")
    eq(log["other_json"].get("billing_source"), "entitlement", "billing_source")
    after = row_of(fulfillment(), CTX["plan_专业版"])
    eq(after["revenue_fen"], before["revenue_fen"], "收入不变")
    eq(after["cost_fen"] - before["cost_fen"], round(7500 / QPU * 7.3 * 100), "成本增加一次调用")


@case("S30", "资金自洽（全部 S 之后）")
def s30():
    assert_consistent("S30")


@case("C4", "授信 + 套餐：权益降级后由授信承担")
def c4():
    lucy = make_user("lucy")
    expect(fund_op(lucy.uid, op="credit_grant", value=1000000, ref="C-lucy", remark="e2e").get("success"), "授信失败")
    CTX["root"].ok("POST", "/api/subscription/admin/bind", {"user_id": lucy.uid, "plan_id": CTX["plan_限速套餐"]})
    sources = []
    for _ in range(3):
        code, _, log = call(lucy, "e2e-chat")
        eq(code, 200, "调用状态")
        sources.append(log["other_json"].get("billing_source"))
    eq(sources, ["entitlement", "entitlement", "wallet"], "前两次走套餐、第三次超速降级")
    eq((log["other_json"].get("entitlement_fallback") or {}).get("reason"), "rate_limited", "降级原因")
    eq(log["credit_consumed"], 15000, "降级的那笔由授信承担（余额为 0）")
    eq(user_row(lucy.uid)["credit_used"], 15000, "欠款")
    assert_consistent("C4")


# ---------------------------------------------------------------------------
# P：用户管理对普通管理员开放、套餐价按实付人民币
# ---------------------------------------------------------------------------

@case("P1", "普通管理员：用户管理与资金操作可用，越权仍被挡")
def p1():
    mgr = make_user("mgr")
    CTX["root"].ok("POST", "/api/user/manage", {"id": mgr.uid, "action": "promote"})
    mgr.login("mgr", "pass12345")  # 角色记在会话里，提权后重新登录
    CTX["mgr"] = mgr

    users = mgr.ok("GET", "/api/user/?p=1&page_size=100")["items"]
    expect(any(u["username"] == "dave" for u in users), "管理员看不到用户列表")
    mgr.ok("GET", "/api/user/search?keyword=dave")

    dave = CTX["dave"].uid
    before = user_row(dave)["points_balance"]
    res = mgr.post("/api/user/manage", {"id": dave, "action": "fund_op", "op": "gift", "value": 1000,
                                        "remark": "P1 管理员赠送"})
    expect(res.get("success"), f"管理员资金操作失败：{res}")
    eq(user_row(dave)["points_balance"] - before, 1000, "管理员赠送到账")

    # 越权：动超管、提权、重置超管 passkey 都必须被挡
    root = CTX["root"].uid
    for body in ({"id": root, "action": "fund_op", "op": "gift", "value": 1000, "remark": "x"},
                 {"id": root, "action": "disable"},
                 {"id": dave, "action": "promote"}):
        r = mgr.post("/api/user/manage", body)
        expect(not r.get("success"), f"越权操作未被挡：{body} → {r}")
    r = mgr.req("DELETE", f"/api/user/{root}/reset_passkey")
    expect(not r.get("success") and "Passkey" not in str(r.get("message")),
           f"管理员重置超管 passkey 应被权限挡下，而不是走到 passkey 查询：{r}")
    # 订阅管理仍是超管专属
    r = mgr.get("/api/subscription/admin/plans")
    expect(not r.get("success"), f"订阅管理不该对普通管理员开放：{r}")
    assert_consistent("P1")


@case("P2", "套餐价按实付人民币：币种一律存 CNY，收款 = price_amount 元")
def p2():
    plan = {"title": "价格口径", "price_amount": 19.9, "currency": "USD", "total_amount": 0,
            "duration_unit": "month", "duration_value": 1, "enabled": True, "quota_reset_period": "never"}
    CTX["root"].ok("POST", "/api/subscription/admin/plans", {"plan": plan, "entitlements": []})
    row = q1("SELECT id, currency, price_amount FROM subscription_plans WHERE title=?", "价格口径")
    eq(row["currency"], "CNY", "传 USD 也存成 CNY")
    listed = CTX["alice"].ok("GET", "/api/subscription/plans")
    p = next(x["plan"] for x in listed if x["plan"]["id"] == row["id"])
    eq((p["currency"], p["price_amount"]), ("CNY", 19.9), "用户侧拿到的币种与价格")
    # 停用，免得影响后面界面用例的套餐卡片
    CTX["root"].ok("PATCH", f"/api/subscription/admin/plans/{row['id']}", {"enabled": False})
