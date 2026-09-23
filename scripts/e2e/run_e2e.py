"""端到端测试驱动：资金对账 + 套餐与算力点。方案见 docs/e2e-test-plan-fund-subscription.md。

前置：scripts/e2e/start_env.sh 已拉起后端（:3300，全新 SQLite）与假上游（:18080）。
用法：python3 scripts/e2e/run_e2e.py [用例前缀...]，如 `F` 只跑资金对账、`S6 S7` 只跑两条。
（用例之间有顺序依赖：初始化必跑，其余按方案顺序累积状态，单独挑跑时需自行保证前置。）
"""
import base64
import hashlib
import json
import os
import sqlite3
import sys
import time
import traceback

import requests

BASE = "http://127.0.0.1:3300"
MOCK = "http://127.0.0.1:18080"
DB_PATH = "/tmp/e2e/e2e.db"
EVIDENCE = "/tmp/e2e/evidence"
EPAY_KEY = "e2e-epay-key"
EPAY_PID = "1001"
QPU = 500000  # QuotaPerUnit
RATE = 7.3
QPCP = 100  # 测试把 1 算力点定为 100 quota

session_proxies = {"http": None, "https": None}  # 本地调用绕开系统代理

# 1x1 png，企业认证 / 实名 / 转账回执用
PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)


# ---------------------------------------------------------------------------
# 基础设施
# ---------------------------------------------------------------------------

class Client:
    """一个登录会话。管理 / 用户接口都要 cookie + New-Api-User 头。"""

    def __init__(self, name):
        self.name = name
        self.s = requests.Session()
        self.s.proxies = session_proxies
        self.uid = None
        self.api_key = None

    def _h(self):
        return {"New-Api-User": str(self.uid)} if self.uid else {}

    def req(self, method, path, **kw):
        r = self.s.request(method, BASE + path, headers=self._h(), timeout=60, **kw)
        try:
            return r.json()
        except ValueError:
            return {"_status": r.status_code, "_text": r.text}

    def get(self, path, **kw):
        return self.req("GET", path, **kw)

    def post(self, path, body=None, **kw):
        return self.req("POST", path, json=body, **kw)

    def put(self, path, body=None, **kw):
        return self.req("PUT", path, json=body, **kw)

    def ok(self, method, path, body=None):
        res = self.req(method, path, json=body)
        if not res.get("success"):
            raise AssertionError(f"{self.name} {method} {path} 失败：{res}")
        return res.get("data")

    def login(self, username, password):
        res = self.s.post(BASE + "/api/user/login", json={"username": username, "password": password}, timeout=30).json()
        if not res.get("success"):
            raise AssertionError(f"登录失败 {username}: {res}")
        self.uid = res["data"]["id"]
        return self

    def relay(self, model, stream=False, max_tokens=None, extra=None):
        """走中继调模型，并等结算落地。返回 (http 状态码, 响应 json 或文本)。

        中继在响应返回**之后**才在后台结算（扣费、透支结转、写日志）。拿到响应就读余额
        会读到中间状态。成功的请求等它的消费日志出现（日志在结算完成后才写）；失败的请求
        没有消费日志，退款也在后台，等一小会儿。
        """
        before = log_count(self.uid)
        code, body = self._relay(model, stream, max_tokens, extra)
        if code == 200:
            deadline = time.time() + 15
            while log_count(self.uid) <= before and time.time() < deadline:
                time.sleep(0.1)
            time.sleep(0.2)
        else:
            time.sleep(1.0)
        return code, body

    def _relay(self, model, stream=False, max_tokens=None, extra=None):
        body = {"model": model, "messages": [{"role": "user", "content": "只回复 ok"}], "stream": stream}
        if max_tokens:
            body["max_tokens"] = max_tokens
        if extra:
            body.update(extra)
        path = "/v1/images/generations" if model.startswith("e2e-image") else "/v1/chat/completions"
        if path.endswith("generations"):
            body = {"model": model, "prompt": "a cat", "n": 1}
        r = requests.post(BASE + path, json=body, timeout=120, proxies=session_proxies,
                          headers={"Authorization": f"Bearer sk-{self.api_key}"}, stream=stream)
        if stream:
            text = "".join(chunk.decode(errors="ignore") for chunk in r.iter_content(chunk_size=None))
            return r.status_code, text
        try:
            return r.status_code, r.json()
        except ValueError:
            return r.status_code, r.text


def db():
    conn = sqlite3.connect(DB_PATH, timeout=30)
    conn.row_factory = sqlite3.Row
    return conn


def q(sql, *args):
    with db() as c:
        return [dict(r) for r in c.execute(sql, args).fetchall()]


def q1(sql, *args):
    rows = q(sql, *args)
    return rows[0] if rows else None


def exec_sql(sql, *args):
    with db() as c:
        c.execute(sql, args)
        c.commit()


def log_count(user_id):
    return q1("SELECT COUNT(*) AS n FROM logs WHERE user_id=? AND type=2", user_id)["n"]


def last_log(user_id):
    row = q1("SELECT * FROM logs WHERE user_id=? AND type IN (2,6) ORDER BY id DESC LIMIT 1", user_id)
    if row:
        row["other_json"] = json.loads(row["other"] or "{}")
    return row


def user_row(uid):
    return q1("SELECT id, username, quota, used_quota, points_balance, points_used, credit_limit, credit_used FROM users WHERE id=?", uid)


def fund_entries(**where):
    cond = " AND ".join(f"{k}=?" for k in where) or "1=1"
    return q(f"SELECT * FROM fund_entries WHERE {cond} ORDER BY id", *where.values())


def epay_pay(client, path, body):
    """易支付下单接口的返回没有 success 字段，成功时 message == "success"。"""
    res = client.post(path, body)
    if res.get("message") != "success":
        raise AssertionError(f"{client.name} {path} 下单失败：{res}")
    return res["data"]


def epay_sign(params):
    """go-epay 的签名：去掉 sign / sign_type 与空值，按 key 排序拼 k=v&k=v，拼上密钥取 md5。"""
    items = sorted((k, v) for k, v in params.items() if k not in ("sign", "sign_type") and v != "")
    raw = "&".join(f"{k}={v}" for k, v in items) + EPAY_KEY
    return hashlib.md5(raw.encode()).hexdigest()


def epay_notify(path, out_trade_no, money, tamper=False):
    params = {
        "pid": EPAY_PID,
        "type": "alipay",
        "out_trade_no": out_trade_no,
        "trade_no": "EP" + out_trade_no,
        "name": "e2e",
        "money": money,
        "trade_status": "TRADE_SUCCESS",
    }
    params["sign"] = epay_sign(params)
    if tamper:
        params["sign"] = ("0" if params["sign"][0] != "0" else "1") + params["sign"][1:]
    params["sign_type"] = "MD5"
    r = requests.post(BASE + path, data=params, timeout=30, proxies=session_proxies)
    return r.text.strip()


# ---------------------------------------------------------------------------
# 用例注册与执行
# ---------------------------------------------------------------------------

CASES = []
RESULTS = []
CTX = {}


def case(cid, title):
    def deco(fn):
        CASES.append((cid, title, fn))
        return fn
    return deco


def expect(cond, msg):
    if not cond:
        raise AssertionError(msg)


def eq(actual, expected, what):
    if actual != expected:
        raise AssertionError(f"{what}：期望 {expected!r}，实际 {actual!r}")


def evidence(cid, name, data):
    os.makedirs(EVIDENCE, exist_ok=True)
    with open(f"{EVIDENCE}/{cid}-{name}.json", "w") as f:
        json.dump(data, f, ensure_ascii=False, indent=2, default=str)


def run(selected):
    for cid, title, fn in CASES:
        if selected and cid != "INIT" and not any(cid.startswith(p) for p in selected):
            continue
        t0 = time.time()
        try:
            note = fn() or ""
            RESULTS.append((cid, title, "PASS", note))
            print(f"PASS {cid:5} {title}  {note}")
        except Exception as e:  # noqa: BLE001 —— 失败要记下来继续跑
            detail = f"{e}" if isinstance(e, AssertionError) else traceback.format_exc(limit=3)
            RESULTS.append((cid, title, "FAIL", detail))
            print(f"FAIL {cid:5} {title}\n      {detail}")
            if cid == "INIT":
                break
        finally:
            _ = time.time() - t0
    passed = sum(1 for r in RESULTS if r[2] == "PASS")
    print(f"\n{passed}/{len(RESULTS)} passed")
    with open(f"{EVIDENCE}/results.json", "w") as f:
        json.dump(RESULTS, f, ensure_ascii=False, indent=2)


# ---------------------------------------------------------------------------
# 初始化：root、定价、渠道、用户
# ---------------------------------------------------------------------------

def set_option(key, value):
    if not isinstance(value, str):
        value = json.dumps(value) if isinstance(value, (dict, list)) else str(value).lower() if isinstance(value, bool) else str(value)
    CTX["root"].ok("PUT", "/api/option/", {"key": key, "value": value})


def make_user(name, aff_code=None):
    body = {"username": name, "password": "pass12345"}
    if aff_code:
        body["aff_code"] = aff_code
    res = requests.post(BASE + "/api/user/register", json=body, timeout=30, proxies=session_proxies).json()
    if not res.get("success"):
        raise AssertionError(f"注册 {name} 失败：{res}")
    c = Client(name).login(name, "pass12345")
    c.ok("POST", "/api/token/", {"name": f"{name}-key", "remain_quota": 0, "unlimited_quota": True, "group": ""})
    tokens = c.ok("GET", "/api/token/?p=0&size=10")
    items = tokens["items"] if isinstance(tokens, dict) else tokens
    tid = items[0]["id"]
    c.api_key = c.ok("POST", f"/api/token/{tid}/key")["key"]
    CTX[name] = c
    return c


def upstream_env():
    env = {}
    with open("/tmp/e2e/upstream.env") as f:
        for line in f:
            if "=" in line:
                k, v = line.strip().split("=", 1)
                env[k] = v
    return env


@case("INIT", "初始化：root、定价、渠道、用户")
def init():
    res = requests.post(BASE + "/api/setup", json={
        "username": "root", "password": "rootpass123", "confirmPassword": "rootpass123",
        "SelfUseModeEnabled": False, "DemoSiteEnabled": False}, timeout=30, proxies=session_proxies).json()
    if not res.get("success") and "已经初始化" not in str(res.get("message")):
        raise AssertionError(f"setup 失败：{res}")
    CTX["root"] = Client("root").login("root", "rootpass123")

    set_option("ModelRatio", {"e2e-chat": 10, "e2e-fail": 10, "e2e-outside": 10, "e2e-limited": 10,
                              "deepseek-v4-flash-0731": 1})
    set_option("CompletionRatio", {"e2e-chat": 1, "e2e-fail": 1, "e2e-outside": 1, "e2e-limited": 1,
                                   "deepseek-v4-flash-0731": 1})
    set_option("ModelPrice", {"e2e-image": 0.04})
    set_option("compute_point_setting.quota_per_compute_point", QPCP)
    # 与现网一致：站点按人民币展示（现网默认 CNY、汇率 7.3）。管理端资金操作弹窗的
    # 金额按展示币种换算，用默认的 USD 跑会得到与现网不同的结果
    set_option("general_setting.quota_display_type", "CNY")
    set_option("PayAddress", "http://127.0.0.1:18080/pay")
    set_option("EpayId", EPAY_PID)
    set_option("EpayKey", EPAY_KEY)

    up = upstream_env()
    chans = [
        {"type": 1, "name": "mock-a", "key": "mock", "base_url": MOCK, "group": "default", "status": 1,
         "models": "e2e-chat,e2e-image,e2e-fail,e2e-outside,e2e-limited"},
        {"type": 1, "name": "mock-b", "key": "mock", "base_url": MOCK, "group": "default", "status": 1,
         "models": "e2e-limited"},
        {"type": 1, "name": "real", "key": up["UPSTREAM_KEY"], "base_url": up["UPSTREAM_BASE_URL"],
         "group": "default", "status": 1, "models": up.get("UPSTREAM_CHAT_MODEL", "deepseek-v4-flash-0731")},
    ]
    for ch in chans:
        CTX["root"].ok("POST", "/api/channel/", {"mode": "single", "channel": ch})
    for row in q("SELECT id, name FROM channels"):
        CTX[f"ch_{row['name']}"] = row["id"]
    CTX["root"].ok("PUT", f"/api/channel/{CTX['ch_mock-a']}/cost",
                   {"costs": [{"model_name": "e2e-chat", "cost_ratio": 0.5, "remark": "e2e"}]})

    for name in ("alice", "bob", "carol", "dave"):
        make_user(name)
    CTX["alice_aff"] = CTX["alice"].ok("GET", "/api/user/aff")
    return f"channels={[CTX[k] for k in CTX if k.startswith('ch_')]}"


# ---------------------------------------------------------------------------
# F：资金对账
# ---------------------------------------------------------------------------

def consistency():
    return CTX["root"].ok("GET", "/api/reconcile/admin/fund/consistency")


def assert_consistent(cid):
    c = consistency()
    evidence(cid, "consistency", c)
    bad = [i for i in c.get("items", []) if not i.get("ok")]
    expect(c.get("all_ok") and not bad, f"自洽校验不平：{bad or c}")


def fund_op(uid, **body):
    return CTX["root"].post("/api/user/manage", {"id": uid, "action": "fund_op", **body})


@case("F1", "期初与空账自洽")
def f1():
    d = CTX["root"].ok("POST", "/api/reconcile/admin/fund/baseline")
    c = consistency()
    evidence("F1", "consistency", c)
    expect(c.get("has_baseline"), f"没有期初：{c}")
    assert_consistent("F1")
    return f"baseline created={d.get('created') if isinstance(d, dict) else d}"


@case("F2", "在线充值（易支付自签回调）")
def f2():
    alice = CTX["alice"]
    before = user_row(alice.uid)
    params = epay_pay(alice, "/api/user/pay", {"amount": 10, "payment_method": "alipay"})
    evidence("F2", "pay", params)
    trade_no = params.get("out_trade_no")
    expect(trade_no, f"支付返回里没有 out_trade_no：{params}")
    money = params.get("money")
    eq(epay_notify("/api/user/epay/notify", trade_no, money), "success", "回调响应")
    after = user_row(alice.uid)
    eq(after["quota"] - before["quota"], 10 * QPU, "alice quota 增量")
    rows = fund_entries(ref_type="topup", ref_id=trade_no)
    evidence("F2", "fund", rows)
    eq(len(rows), 1, "流水条数")
    r = rows[0]
    eq((r["account"], r["kind"], r["source"]), ("cash", "prepay", "online_pay"), "流水性质")
    eq(r["cash_fen"], 7300, "cash_fen")
    CTX["f2_trade"] = (trade_no, money)
    return f"trade={trade_no} money={money}"


@case("F3", "支付回调幂等")
def f3():
    trade_no, money = CTX["f2_trade"]
    before = user_row(CTX["alice"].uid)["quota"]
    for _ in range(2):
        epay_notify("/api/user/epay/notify", trade_no, money)
    eq(user_row(CTX["alice"].uid)["quota"], before, "重复回调后 quota")
    eq(len(fund_entries(ref_type="topup", ref_id=trade_no)), 1, "流水条数")


@case("F4", "回调签名错误")
def f4():
    alice = CTX["alice"]
    params = epay_pay(alice, "/api/user/pay", {"amount": 5, "payment_method": "alipay"})
    before = user_row(alice.uid)["quota"]
    eq(epay_notify("/api/user/epay/notify", params["out_trade_no"], params["money"], tamper=True), "fail", "篡改签名的回调")
    eq(user_row(alice.uid)["quota"], before, "quota")
    eq(len(fund_entries(ref_type="topup", ref_id=params["out_trade_no"])), 0, "流水条数")


@case("F5", "管理员现金入账")
def f5():
    bob = CTX["bob"]
    res = fund_op(bob.uid, op="prepay", value=1000000, cash_fen=1460, remark="e2e")
    expect(not res.get("success"), f"缺 ref 应被拒：{res}")
    before = user_row(bob.uid)["quota"]
    res = fund_op(bob.uid, op="prepay", value=1000000, cash_fen=1460, ref="R1", remark="e2e")
    expect(res.get("success"), f"入账失败：{res}")
    eq(user_row(bob.uid)["quota"] - before, 1000000, "bob quota 增量")
    rows = fund_entries(user_id=bob.uid, kind="prepay", source="admin_cash")
    evidence("F5", "fund", rows)
    eq(len(rows), 1, "流水条数")
    eq((rows[0]["account"], rows[0]["cash_fen"], rows[0]["quota_delta"]), ("cash", 1460, 1000000), "流水")


@case("F6", "管理员赠送积分")
def f6():
    dave = CTX["dave"]
    res = fund_op(dave.uid, op="gift", value=68493, remark="e2e gift")
    expect(res.get("success"), f"赠送失败：{res}")
    eq(user_row(dave.uid)["points_balance"], 68493, "dave points_balance")
    rows = fund_entries(user_id=dave.uid, kind="gift", source="admin_gift")
    eq((rows[0]["account"], rows[0]["cash_fen"]), ("points", 0), "流水")


@case("F7", "授信开额")
def f7():
    carol = CTX["carol"]
    res = fund_op(carol.uid, op="credit_grant", value=5000000, ref="C1", remark="e2e")
    expect(res.get("success"), f"授信失败：{res}")
    u = user_row(carol.uid)
    eq(u["credit_limit"], 5000000, "credit_limit")
    rows = fund_entries(user_id=carol.uid, kind="credit_grant")
    eq((rows[0]["account"], rows[0]["quota_delta"], rows[0]["cash_fen"]), ("credit", 5000000, 0), "流水")


@case("F8", "授信消费（余额为 0）")
def f8():
    carol = CTX["carol"]
    eq(user_row(carol.uid)["quota"], 0, "carol 初始 quota")
    for i in range(3):
        code, body = carol.relay("e2e-outside")
        eq(code, 200, f"第 {i + 1} 次调用状态")
    u = user_row(carol.uid)
    evidence("F8", "user", u)
    eq(u["quota"], 0, "透支结转后 quota")
    eq(u["credit_used"], 45000, "credit_used")
    log = last_log(carol.uid)
    eq(log["credit_consumed"], 15000, "日志 credit_consumed")
    eq(log["other_json"].get("billing_source"), "wallet", "billing_source")


@case("F9", "授信用尽后拒绝")
def f9():
    carol = CTX["carol"]
    used = user_row(carol.uid)["credit_used"]
    res = fund_op(carol.uid, op="credit_grant", value=used - 1, ref="C2", remark="e2e")
    expect(not res.get("success"), f"额度低于已用额应被拒：{res}")
    res = fund_op(carol.uid, op="credit_grant", value=used + 10000, ref="C3", remark="e2e")
    expect(res.get("success"), f"调额失败：{res}")
    # 预扣按 max_tokens 估算：(prompt + 5000) × 10 远超剩余 10000，必须在预扣时就被拒
    code, body = carol.relay("e2e-outside", max_tokens=5000)
    evidence("F9", "relay", {"code": code, "body": body})
    expect(code != 200, f"额度不足却调用成功：{code} {body}")
    eq(user_row(carol.uid)["credit_used"], used, "credit_used 不变")
    return f"status={code}"


@case("F10", "信用回款")
def f10():
    carol = CTX["carol"]
    used = user_row(carol.uid)["credit_used"]
    res = fund_op(carol.uid, op="ar_settle", value=used + 1, cash_fen=66, ref="S0", remark="e2e")
    expect(not res.get("success"), f"超过已用额应被拒：{res}")
    res = fund_op(carol.uid, op="ar_settle", value=used, cash_fen=66, ref="S1", remark="e2e")
    expect(res.get("success"), f"回款失败：{res}")
    eq(user_row(carol.uid)["credit_used"], 0, "credit_used")
    rows = fund_entries(user_id=carol.uid, kind="ar_settle")
    eq((rows[0]["account"], rows[0]["cash_fen"], rows[0]["quota_delta"]), ("credit", 66, -used), "流水")
    assert_consistent("F10")


@case("F11", "积分混扣")
def f11():
    set_option("points_setting.enabled", True)
    set_option("points_setting.require_kyc", False)
    set_option("points_setting.enabled_groups", ["default"])
    dave = CTX["dave"]
    before = user_row(dave.uid)
    code, body = dave.relay("e2e-chat")
    eq(code, 200, "调用状态")
    after = user_row(dave.uid)
    log = last_log(dave.uid)
    evidence("F11", "log", log)
    eq(log["other_json"].get("billing_source"), "points_wallet", "billing_source")
    expect(log["points_consumed"] >= 15000, f"积分抵扣应覆盖 15000：{log['points_consumed']}")
    eq(before["points_balance"] - after["points_balance"], log["points_consumed"], "积分余额减少量")
    eq(after["quota"], before["quota"], "钱包不动（积分足够）")
    return f"points_consumed={log['points_consumed']}"


@case("F12", "兑换码（积分）")
def f12():
    keys = CTX["root"].ok("POST", "/api/redemption/", {"name": "e2e", "count": 1, "quota": 6849, "reward_type": "points"})
    key = keys[0] if isinstance(keys, list) else keys
    dave = CTX["dave"]
    before = user_row(dave.uid)["points_balance"]
    res = dave.post("/api/user/topup", {"key": key})
    expect(res.get("success"), f"兑换失败：{res}")
    eq(user_row(dave.uid)["points_balance"] - before, 6849, "积分增量")
    rows = fund_entries(user_id=dave.uid, source="redemption")
    eq((rows[0]["account"], rows[0]["kind"], rows[0]["cash_fen"]), ("points", "gift", 0), "流水")


@case("F13", "签到（积分）")
def f13():
    set_option("checkin_setting.enabled", True)
    set_option("checkin_setting.reward_type", "points")
    set_option("checkin_setting.min_points", 5)
    set_option("checkin_setting.max_points", 5)
    dave = CTX["dave"]
    before = user_row(dave.uid)["points_balance"]
    res = dave.post("/api/user/checkin")
    expect(res.get("success"), f"签到失败：{res}")
    res2 = dave.post("/api/user/checkin")
    expect(not res2.get("success"), f"同日重复签到应被拒：{res2}")
    rows = fund_entries(user_id=dave.uid, source="checkin")
    eq(len(rows), 1, "签到流水条数")
    eq(rows[0]["account"], "points", "账户")
    gained = user_row(dave.uid)["points_balance"] - before
    eq(gained, rows[0]["quota_delta"], "积分增量与流水一致")
    return f"gained={gained}"


@case("F14", "注册礼（积分）")
def f14():
    set_option("points_setting.new_user_points", 10)
    frank = make_user("frank")
    rows = fund_entries(user_id=frank.uid, source="register")
    evidence("F14", "fund", rows)
    eq(len(rows), 1, "注册礼流水条数")
    eq((rows[0]["account"], rows[0]["kind"]), ("points", "gift"), "流水")
    eq((rows[0]["ref_type"], rows[0]["ref_id"]), ("user", f"register-points:{frank.uid}"), "ref")
    eq(user_row(frank.uid)["points_balance"], rows[0]["quota_delta"], "积分余额")
    set_option("points_setting.new_user_points", 0)


def valid_id_number():
    base = "11010519900307"  # 地区 + 生日
    body = base + "123"
    weights = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2]
    codes = "10X98765432"
    return body + codes[sum(int(a) * b for a, b in zip(body, weights)) % 11]


@case("F15", "实名与邀请奖励")
def f15():
    set_option("points_setting.kyc_verified_points", 20)
    set_option("points_setting.kyc_inviter_points", 30)
    erin = make_user("erin", aff_code=CTX["alice_aff"])
    img = PNG_B64
    res = erin.post("/api/user/kyc", {"real_name": "测试用户", "id_type": "id_card", "id_number": valid_id_number(),
                                      "id_card_front": img, "id_card_back": img})
    expect(res.get("success"), f"提交实名失败：{res}")
    kyc = CTX["root"].ok("GET", f"/api/user/kyc/admin/by-user/{erin.uid}")
    kyc_id = kyc.get("id") if isinstance(kyc, dict) else None
    expect(kyc_id, f"找不到实名记录：{kyc}")
    CTX["root"].ok("PUT", f"/api/user/kyc/admin/{kyc_id}/approve")
    self_rows = fund_entries(user_id=erin.uid, source="kyc")
    inv_rows = fund_entries(user_id=CTX["alice"].uid, source="invite")
    evidence("F15", "fund", {"self": self_rows, "inviter": inv_rows})
    eq(len(self_rows), 1, "实名奖励流水")
    eq(len(inv_rows), 1, "邀请奖励流水")
    eq(inv_rows[0]["ref_id"], f"invite:{erin.uid}", "邀请流水 ref（指向被邀请人）")


@case("F16", "对公转账")
def f16():
    for k, v in {"bank_transfer_setting.enabled": True, "bank_transfer_setting.company_name": "E2E 公司",
                 "bank_transfer_setting.payee_name": "E2E 公司", "bank_transfer_setting.account_number": "6222000000000000",
                 "bank_transfer_setting.bank_name": "测试银行"}.items():
        set_option(k, v)
    bob = CTX["bob"]
    img = PNG_B64
    res = bob.post("/api/user/enterprise", {"company_name": "测试科技有限公司", "uscc": "91110105MA00000000",
                                            "legal_rep_name": "张三", "legal_rep_id": valid_id_number(),
                                            "license": img, "legal_front": img, "legal_back": img})
    expect(res.get("success"), f"提交企业认证失败：{res}")
    ent = q1("SELECT id FROM user_enterprises WHERE user_id=?", bob.uid)
    expect(ent, "找不到企业认证记录")
    CTX["root"].ok("PUT", f"/api/user/enterprise/admin/{ent['id']}/approve")
    before = user_row(bob.uid)["quota"]
    res = bob.post("/api/user/bank_transfer", {"amount_fen": 7300, "receipt": img, "remark": "e2e"})
    expect(res.get("success"), f"提交转账失败：{res}")
    order = q1("SELECT id, trade_no FROM bank_transfer_orders WHERE user_id=? ORDER BY id DESC", bob.uid)
    CTX["root"].ok("PUT", f"/api/user/bank_transfer/admin/{order['id']}/approve", {"review_remark": "e2e"})
    eq(user_row(bob.uid)["quota"] - before, 5000000, "bob quota 增量")
    rows = fund_entries(user_id=bob.uid, source="bank_transfer")
    eq((rows[0]["account"], rows[0]["kind"], rows[0]["cash_fen"]), ("cash", "prepay", 7300), "流水")


@case("F17", "收入对账报表")
def f17():
    s = CTX["root"].ok("GET", "/api/reconcile/admin/fund/summary")
    evidence("F17", "summary", s)
    revenue = sum(r["cash_fen"] for r in fund_entries() if r["kind"] in ("prepay", "ar_settle"))
    eq(s["revenue"]["cash_fen_total"], revenue, "真实入账")
    logs = q("SELECT quota, points_consumed, credit_consumed, type, other FROM logs WHERE type IN (2,6)")
    total = 0
    for l in logs:
        o = json.loads(l["other"] or "{}")
        if o.get("billing_source") in ("subscription", "entitlement"):
            continue
        total += l["quota"] if l["type"] == 2 else -l["quota"]
    eq(s["consume"]["total_quota"], total, "消费总额")
    return f"revenue_fen={revenue} consume={total}"


@case("F18", "资金自洽（全部 F 之后）")
def f18():
    assert_consistent("F18")


@case("F19", "流水查询与导出")
def f19():
    data = CTX["root"].ok("GET", "/api/reconcile/admin/fund/entries?account=points&kind=gift&p=1&page_size=100")
    items = data.get("items", [])
    expected = len(fund_entries(account="points", kind="gift"))
    eq(len(items), expected, "积分赠送流水条数")
    r = CTX["root"].s.get(BASE + "/api/reconcile/admin/fund/export",
                          headers={"New-Api-User": str(CTX["root"].uid)}, timeout=30)
    expect(r.content.startswith(b"\xef\xbb\xbf"), "CSV 没有 BOM")
    lines = r.content.decode("utf-8-sig").strip().splitlines()
    expect(len(lines) - 1 >= len(fund_entries()) - 5, f"CSV 行数异常：{len(lines)}")
    return f"csv_lines={len(lines)}"


# ---------------------------------------------------------------------------
# C：授信补充用例（方案 §3.1）
# ---------------------------------------------------------------------------

def credit_user(name, prepay=0, gift=0, credit=0):
    u = make_user(name)
    if prepay:
        expect(fund_op(u.uid, op="prepay", value=prepay, cash_fen=1, ref=f"R-{name}", remark="e2e").get("success"), "入账失败")
    if gift:
        expect(fund_op(u.uid, op="gift", value=gift, remark="e2e").get("success"), "赠送失败")
    if credit:
        expect(fund_op(u.uid, op="credit_grant", value=credit, ref=f"C-{name}", remark="e2e").get("success"), "授信失败")
    return u


@case("C1", "部分透支：余额先扣光，其余记欠款")
def c1():
    gina = credit_user("gina", prepay=5000, credit=1000000)
    code, _ = gina.relay("e2e-outside")
    eq(code, 200, "调用状态")
    u, log = user_row(gina.uid), last_log(gina.uid)
    evidence("C1", "state", {"user": u, "log": log})
    eq(u["quota"], 0, "余额扣光")
    eq(u["credit_used"], 10000, "欠款 = 15000 − 5000")
    eq(log["credit_consumed"], 10000, "日志授信承担")
    eq(log["other_json"].get("billing_source"), "wallet", "billing_source")


@case("C2", "结算冲破授信上限：敞口止于一笔，下一次被拒")
def c2():
    hank = credit_user("hank", credit=10000)
    # 不带 max_tokens：预扣只按估算的输入部分，能过；实际花费 15000 超过剩余授信 10000
    code, _ = hank.relay("e2e-outside")
    eq(code, 200, "第一次调用状态（服务已交付，结算不能失败）")
    u = user_row(hank.uid)
    log = last_log(hank.uid)
    evidence("C2", "after-first", {"user": u, "log": log})
    eq(u["credit_used"] - u["quota"], 15000, "这一笔全部有着落（欠款 + 负余额 = 15000）")
    eq(log["credit_consumed"], u["credit_used"], "日志记的授信承担与欠款一致")
    code2, body2 = hank.relay("e2e-outside")
    evidence("C2", "second", {"code": code2, "body": body2})
    expect(code2 != 200, f"超限之后下一次必须被拒：{code2} {body2}")
    eq(user_row(hank.uid), u, "被拒的请求不动余额与欠款")
    CTX["c2_state"] = u
    return f"credit_limit={u['credit_limit']} credit_used={u['credit_used']} quota={u['quota']}"


@case("C2b", "用户能看到自己的授信额度与欠款")
def c2b():
    me = CTX["hank"].ok("GET", "/api/user/self")
    eq((me.get("credit_limit"), me.get("credit_used")), (10000, 15000), "hank 的额度与欠款（C2 之后超限）")
    other = CTX["gina"].ok("GET", "/api/user/self")
    eq((other.get("credit_limit"), other.get("credit_used")), (1000000, 10000), "gina 的额度与欠款")


@case("C3", "授信 + 积分混扣：积分 → 余额 → 授信")
def c3():
    ivan = credit_user("ivan", gift=5000, credit=1000000)
    code, _ = ivan.relay("e2e-outside")
    eq(code, 200, "调用状态")
    u, log = user_row(ivan.uid), last_log(ivan.uid)
    evidence("C3", "state", {"user": u, "log": log})
    eq(log["other_json"].get("billing_source"), "points_wallet", "billing_source")
    eq(log["points_consumed"], 5000, "积分先扣光（余额不够取整，不多扣）")
    eq(log["credit_consumed"], 10000, "其余由授信承担")
    eq((u["points_balance"], u["quota"], u["credit_used"]), (0, 0, 10000), "三个账户")
    assert_consistent("C3")


if __name__ == "__main__":
    # 套餐部分的用例在 run_e2e_subscription.py 里注册（按顺序追加）
    try:
        import run_e2e_subscription  # noqa: F401
    except ImportError:
        pass
    run(sys.argv[1:])
