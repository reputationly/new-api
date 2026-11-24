# 全局模型定价汇率：方案 B 与升级手册

## 计价约定

- `ModelSourceRate` 从「模型 → 汇率」JSON 表改为一个全局正数（人民币/美元，未配置时无值）；不读、不改 `USDExchangeRate` 或 `QuotaPerUnit`。人民币仍按 `QuotaPerYuan` 记账。
- 用 `ModelPriceCurrency`（建议配置键）记录模型定价币种，缺省为 CNY。USD 模型存美元目录原价；按量锚点、按次价、分时绝对价、视频矩阵在计费入口换成人民币。相对倍率（例如补全、缓存、分组）、规则阈值和时间不换算。通配价格和代码默认价始终要能确定其原始币种：首版只允许对具体模型显式切 USD，并为该模型当前依赖的共享或默认价格建立明确的独立价，不把同一个通配价格表项按两个币种解释。
- 表达式继续使用 `v2:`，求值结果仍是人民币：CNY 表达式不引用 `fx`；USD 表达式显式使用 `fx` 折算美元系数，由全局值供给，不对结果再次乘汇率。编辑器应提示每个美元收费项都要换算；检测到引用不代表每一项都正确，手写混合算式要人工核对。使用实际全局汇率做保存校验（含修改全局汇率时重验所有引用 `fx` 的表达式；不能保证冒烟向量覆盖所有分支，保留运行时校验）。
- 同步、WebSocket 和异步任务统一在对上游发起请求前校验并冻结币种、汇率及有效价格；USD 模型缺有效全局汇率时**拒绝请求**（包括免预扣请求），不默认 1、不发上游、不扣费。既有有效 USD 配置时也不允许把汇率清空。同步、轮询补扣、退款及历史重渲染只用请求快照；旧任务没有新快照时保持原有人民币结算，不读新汇率。现有是多副本内存配置，应以一致的币种/价表/汇率版本切换，发布刷新后新请求生效；不能承诺旧进程未刷新时也立即切价。
- 模型广场及计费都使用同一换算规则。给前端提供明确的人民币等值与有效币种信息，避免展示层把已经折算过的价格再乘一次；USD 汇率缺失时显示「未配置/不可用」，不能显示零价。消费日志和任务详情记录当时币种、实际汇率及人民币有效价（保留 `price_unit: cny` 语义）；已有表达式日志保留 `expr_b64` / `expr_fx`。
- 配置操作继续用定价管理审计（使用日志 → 管理；不是给每个模型补写任务日志）。改全局汇率只产生一条管理记录、一个 `ModelSourceRate` 变更项，记旧值/新值与操作人、时间，不重写任何价格。表达式改动按模型记 `billing_setting.billing_expr` 原文前后值（含版本及请求规则），不改换算后的虚构系数。改币种并转换价格则记录该模型币种及本次实际写入项。用 `controller/option.go:193–208`、`model/option.go:348–405` 的最终状态事务和 `model/option_admin_audit.go:50–79` 的差异机制；全局汇率必须独立提交，避免客户端顺手重发价表。

## 升级前：只读盘点（PostgreSQL 14）

在现网目标库的 SQL 客户端执行以下整段，仅访问 `options` 三个定价键。结果列出**所有**有旧汇率的模型和**疑似**引用 `fx` 的表达式，含非当前启用的表达式。筛选是词边界匹配，会命中档位名或字符串中的 `fx`；用应用的解析器确认实际变量（基础表达式用 `pkg/billingexpr/compile.go:158` 的 `UsedVars`，如含 `|||` 再核对请求规则），不要根据 SQL 结果直接做文本替换。无匹配返回零行；如果非空值不是有效的 JSON 对象，语句会报错，应停止迁移并检查旧数据，不能当成空表。

```sql
BEGIN READ ONLY;
WITH option_entries AS (
    SELECT o."key" AS option_key, j.model, j.value
    FROM options AS o
    CROSS JOIN LATERAL jsonb_each(
        COALESCE(NULLIF(NULLIF(btrim(o.value), '')::jsonb, 'null'::jsonb), '{}'::jsonb)
    ) AS j(model, value)
    WHERE o."key" IN ('ModelSourceRate', 'billing_setting.billing_expr', 'billing_setting.billing_mode')
),
rates AS (
    SELECT model, value AS old_source_rate
    FROM option_entries
    WHERE option_key = 'ModelSourceRate'
),
fx_candidates AS (
    SELECT model, value #>> '{}' AS expression
    FROM option_entries
    WHERE option_key = 'billing_setting.billing_expr'
      AND (value #>> '{}') ~ '[[:<:]]fx[[:>:]]'
)
SELECT COALESCE(r.model, f.model) AS model,
       r.old_source_rate,
       m.value #>> '{}' AS billing_mode,
       f.expression IS NOT NULL AS fx_candidate,
       f.expression
FROM rates AS r
FULL JOIN fx_candidates AS f USING (model)
LEFT JOIN option_entries AS m
    ON m.model = COALESCE(r.model, f.model)
   AND m.option_key = 'billing_setting.billing_mode'
ORDER BY 1;
COMMIT;
```

## 方案 B：先全部保持人民币，再逐个切美元

### 本次现网的等价迁移

用户提供的盘点结果中，只有 `gemini-nano-banana-2.1`、`gpt-5.5`、`gpt-5.6-luna`、`gpt-5.6-sol`、`gpt-5.6-terra` 配过源汇率，均为 6.8，且均使用含 `fx` 的 `tiered_expr`。这五个模型的表达式已经保留美元目录价，无需先把表达式改成人民币：迁移取全局值 6.8，保留表达式原文及美元计价。迁移不设置模型币种，表达式中的 `fx` 与普通定价币种独立，已有普通价格（包括这五个模型的备用价格）和工具价仍默认 CNY。

自动迁移遇到旧 JSON 表时，只接受全部配置值为同一个有效正数；取该值写入全局标量。空表不设置汇率；汇率不同或任一旧值无效则拒绝迁移，报错由运营核对、处理后重试，不能擅自取第一个或取平均。迁移前备份旧 JSON 及相关定价配置，回滚先恢复旧格式和相关选项再启动旧二进制；部署时旧版本与新版本不能并行读写。上线后分别选取 `len = 272000` 和 `len = 272001`，用同样请求条件和 token 用量对比 `gpt-5.5` 的预扣与实扣；两档输入/输出/缓存单价应分别仍为 ¥34/204/3.4 和 ¥68/306/6.8 每百万 tokens，消费日志的 `expr_fx` 应仍是 6.8。

### 其他旧环境需要先人民币化时

1. **升级前**：先核对运行版本确实使用人民币口径，并备份完整定价 options、旧汇率表、表达式及模式。现版美元录入开关只是浏览器偏好，基础价、分时价、视频矩阵和工具价已存人民币；切关可以方便核对，但无需重录、乘汇率或批量改价（`web/classic/src/pages/Setting/Ratio/components/PriceInput.jsx:23–51,75–109`、`ModelPricingEditor.jsx:1086–1099`）。不要为了「改人民币」去改旧源汇率，它会触发旧编辑器重算基础价。
2. **真正需要转换的是引用 `fx` 的表达式**：按每个模型原有的有效汇率，把实际的 `fx` 变量固定为等值数值。例如旧率 6.8，`v2:tier("base", p * 2 * fx)` 可改成 `v2:tier("base", p * 13.6)`。复杂表达式将真实变量替换为带括号数值并保留其他逻辑；不要替换字符串、阈值、无关乘数。用原汇率对照两版的预扣和结算，覆盖档位、时间、请求规则。缺有效旧率、历史版本表达式或定价基线不明，先人工确认，不猜测补值；停用的表达式也须改好、或备份后从活动配置移除，避免新版本保存时被拦。
3. **切换版本**：冻结定价写入；复查已无待处理的真实 `fx` 引用，对备份做恢复演练。旧版本读的是 map，新版本写的是标量，不能让两代进程同时读写同一个键。协调停止旧版本流量/写入，按方案 B 让未指定币种的模型默认 CNY、不改金额；归档旧 map，新版本启用独立全局标量。回滚须恢复旧格式的选项和旧版本能读的价格，不能只换二进制；逐模型切 USD 后尤其要避免旧进程把美元当人民币。长任务要么保留旧快照兼容，要么上线前清空不兼容任务。
4. **升级后**：设置有效的全局汇率，核对 CNY 模型预扣、结算、展示未变化，再逐模型切 USD。运营可按「当前人民币价 ÷ 当前全局汇率」预览保价切换，或输入供应商的真实美元目录价（后者可能改价）；切币种和价格同一次保存。示例：¥13.60 在 6.8 下切为 $2；改全局汇率到 7.0 后，存储仍是 $2，新请求扣 ¥14，旧请求继续按 ¥13.60。复杂表达式只能在核对每个收费项后改回显式 `fx`；不把整条表达式里的数字统一除以汇率。

## 全覆盖的改动范围

| 路径 | 工作量与落点 |
| --- | --- |
| **按量 / 按次** | **中等**。在 `relay/helper/price.go:79–181,191–249` 生成快照时折算价格锚点；审核 `service/text_quota.go:235–319`、`service/quota.go:53–171,308–420`（特别是 WebSocket 的 `PreWssConsumeQuota` 会直接取倍率）、`service/task_billing.go:638–702` 的任务倍率回退。不要在会话中重新读汇率或把无量纲倍率乘两次；Gemini 音频差价及 OpenRouter 成本反推需有专门用例，后者的成本换算仍使用独立的 `USDExchangeRate`（`service/quota.go:282–305`）。 |
| **分段表达式** | **中等**。已有 `ExprFX` 快照，主要改取值与校验：`setting/billing_setting/tiered_billing.go:116–124,230–265`、`relay/helper/price.go:272–350`、`pkg/billingexpr/settle.go:84–85`、`service/log_info_generate.go:373–383`。不用改表达式引擎的人民币额度换算，也不能把同一全局汇率乘两遍。 |
| **分时价** | **中等**。`setting/ratio_setting/group_time_ratio.go:195–269` 用绝对价 ÷ 基础价派生因子；分子分母必须同币种，只对最终有效绝对价折算一次。切币种时要盘点该模型所有作用域的绝对价，包括命中的共享规则；不能直接改其他模型共用的规则。继续禁止分段表达式与分时配置叠加。 |
| **视频矩阵 / 异步任务** | **较难**。`relay/video_billing.go:131–225` 覆盖 token、秒、次；`controller/relay.go:757–768` 已落任务快照。`service/task_billing.go:727–761,780–799` 在提交时缺档位时会按上游实际分辨率重新读矩阵：改成在提交时冻结可选矩阵价格（或可重读的不可变价表版本）及汇率，轮询不能用现价或现汇率。还要测矩阵未命中回退、免费分组、补扣和退款。 |
| **工具调用** | **中偏难，必须覆盖**。建议工具目录每条价格带独立币种（默认价与前缀覆盖各自配置，存量均 CNY），美元工具价共用同一个全局汇率。共享工具默认价不能只因调用模型切 USD 就变币种；需要随某款模型变动时配相应覆盖项。路径：`setting/operation_setting/tools.go:22–35,118–145,165–207`（硬编码图片附加费也要有明确单位）、`service/text_quota.go:84–139,243`（实际附加费路径）、`service/tool_billing.go:39–87`（仓内还有独立工具计价函数，未搜到非测试调用，也应避免两套换算）。按千次和按次分别折算，最长前缀优先；请求前冻结可能使用的工具有效价及汇率，不能等返回发现美元工具价无有效汇率后才拒绝或免费。人民币模型已知会使用无有效汇率的美元工具也应预先拦截。 |
| **展示 / 审计** | **中等**。`model/pricing.go:96–138,460–498` 和 `controller/pricing.go:152–153` 的缓存和展示应与计费一起使用同一份定价版本；前端编辑器移除「改每模型源汇率会批量缩放」逻辑（`useModelPricingEditorState.js:1384–1435`）。分清模型广场按当前配置展示、使用日志/任务按历史快照展示、管理日志只记实际配置变更。 |

**拆分建议**：先做币种/全局配置、快照和方案 B 兼容，再接普通价、分时价与表达式，然后接异步视频和工具调用，最后统一展示和审计。可以拆 PR 交付，但不要在某条路径仍会把美元当人民币时开放 USD 切换。验收重点：所有路径 CNY 无回归、USD 只乘一次、汇率缺失在上游前拒绝、改汇率新单变价但在途/历史不变、表达式只记文本差异、改汇率只写全局键、工具默认与前缀覆盖分别选币种、视频回执补查与退款用提交版本。业务实现须支持 SQLite/MySQL/PostgreSQL；上面的盘点 SQL 只供 PostgreSQL 运维，不进入业务迁移代码。
