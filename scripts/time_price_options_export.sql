-- 时段价改造：现网配置导出（PostgreSQL，只读）
--
-- 目的：拿到关键 option 的当前值，离线算「保价 / 纠正」的影响面。
-- 表：options ("key" text 主键, "value" text)，GORM 默认复数表名。只读账号即可。

-- 1) 一条 SQL 导出全部相关 option（结果是一行 JSON，整体拷走即可）
SELECT jsonb_object_agg("key", "value") AS options_json
FROM options
WHERE "key" IN (
    'ModelRatio',          -- 目录价（标准价目）
    'ModelPrice',          -- 按次计费固定价
    'GroupRatio',          -- 线路基准倍率
    'GroupGroupRatio',     -- 身份覆盖（判断 Base 是否随用户档变）
    'GroupModelRatio',     -- 模型定价（multiply / override）
    'UserGroupModelRatio', -- 用户档折扣（仅用于展示终值，不影响相对关系）
    'GroupTimeRatio',      -- 时段规则（本次改造对象）
    'GroupEnabled',        -- 停用线路（判断规则是否休眠）
    'billing_setting.billing_mode',  -- 是否 tiered_expr（注意带前缀：这两项走 config 扁平化，不是顶层 key）
    'billing_setting.billing_expr'   -- 表达式内容（检查有没有 hour()/weekday()）
);

-- 2) 先确认 key 名没写错、值有多大（防导出被截断）
SELECT "key",
       pg_size_pretty(length("value")::bigint) AS size,
       length("value")                          AS bytes
FROM options
WHERE "key" LIKE '%Ratio%'
   OR "key" LIKE 'ModelBilling%'
   OR "key" = 'GroupEnabled'
ORDER BY "key";

-- 3) psql 下直接落文件（推荐，避免长 JSON 拷不全）
--    \copy (SELECT jsonb_object_agg("key","value") FROM options WHERE "key" IN (...)) TO 'options.json'
--
--    或逐 key 落文件，脚本也支持「目录 + 选项名.json」的输入形态：
--    \copy (SELECT "value" FROM options WHERE "key"='GroupTimeRatio') TO 'options/GroupTimeRatio.json'
--    \copy (SELECT "value" FROM options WHERE "key"='GroupModelRatio') TO 'options/GroupModelRatio.json'

-- 4) 只想知道「有没有配时段规则」时，不必导全量
SELECT CASE WHEN "value" IN ('', '{}', 'null') THEN '未配置' ELSE '已配置' END AS state,
       pg_size_pretty(length("value")::bigint) AS size
FROM options WHERE "key" = 'GroupTimeRatio';
