# 生产环境高可用迁移指南

**实际执行结果（2026-04-26）：**
- PostgreSQL：已迁移至华为云 RDS 主备版（PG 14），数据层高可用已实现 ✅
- Redis：维持单容器方案，原因见文末「当前架构状态与后续注意事项」

---

本文档原始计划为将 Redis 和 PostgreSQL 均迁移至云托管主备服务。Redis 部分因以下原因暂不执行：Redis 在本项目中仅作缓存使用，所有数据均可从 PostgreSQL 重建，单容器故障的业务影响可接受；可用的云 DCS 服务版本（3.0）与代码不兼容（见兼容性分析章节），自建 HA Redis 的运维成本超出收益。

---

## 目录

1. [架构变化](#架构变化)
2. [兼容性分析](#兼容性分析)
3. [需要修改的文件](#需要修改的文件)
4. [迁移前准备](#迁移前准备)
5. [数据迁移步骤](#数据迁移步骤)
6. [生产切换操作](#生产切换操作)
7. [降级后注意事项](#降级后注意事项)
8. [回滚方案](#回滚方案)
9. [验证清单](#验证清单)

---

## 架构变化

### 迁移前

```
应用服务器（单台）
├── new-api 容器
├── watchtower 容器
├── postgres:15 容器（本地卷 pg_data）
└── redis:latest 容器
```

### 实际迁移后（当前生产状态）

```
应用服务器（BMS 裸金属）             云托管数据层
├── new-api 容器   ──────────────▶  华为云 RDS PostgreSQL 14 主备版
├── redis 容器（单容器，本机）
└── watchtower 容器
```

**核心原则**

- PostgreSQL 由华为云 RDS 负责主备切换，SLA 由云平台保障，应用层感知不到切换
- Redis 仍为本机单容器，仅作缓存；故障时缓存丢失，应用自动从 PostgreSQL 重建，业务不中断
- Redis 连接串使用容器服务名（`redis://redis`），无需配置外部地址

---

## 兼容性分析

### PostgreSQL 15（容器）→ 14（云 RDS）

属于版本降级，需要特别注意。

**结论：安全，有前提条件。**

| 检查项 | 结果 | 说明 |
|---|---|---|
| 代码中的 raw SQL | ✅ 兼容 | `ALTER COLUMN TYPE`、`information_schema` 查询、`USING` 转换均为 PG9.1+ 标准语法 |
| GORM AutoMigrate | ✅ 兼容 | GORM 生成标准 DDL，无版本特定语法 |
| PG15 独有特性 | ✅ 未使用 | `MERGE`、`NULLS NOT DISTINCT`、`security_invoker` 均未出现在代码中 |
| dump 格式 | ⚠️ 有要求 | **必须使用 plain 格式**（`pg_dump` 默认即是），不能用 `-Fc` 二进制格式 |
| 数据库用户权限 | ⚠️ 需确认 | 云 RDS 用户需要有 `ALTER TABLE` 权限，GORM 启动时会执行 migration |

**为什么 plain 格式是必须的：**

```
pg_dump（默认，-Fp）  →  输出纯 SQL 文本  →  psql 执行  →  无版本校验，PG14 可用 ✅
pg_dump -Fc           →  输出二进制格式   →  pg_restore  →  拒绝高版本 dump，失败 ❌
```

### Redis latest（≈ 7.x，容器）→ 云服务 Redis

> ⚠️ **版本要求：最低 Redis 4.0，建议 5.0+。Redis 3.0 不可用，原因见下方详细分析。**

#### Redis 3.0：不兼容，核心缓存完全失效

`common/redis.go` 中 `RedisHSetObj` 函数（第 147 行）将结构体展开为 `map[string]interface{}`，通过 go-redis v8 发送多字段 HSET：

```
HSET user:1 Id 1 Group default Email x@x.com Quota 1000 Status 1 Username foo Setting {}
```

**多字段 HSET 是 Redis 4.0 才引入的语法**。Redis 3.0 收到此命令直接返回：
```
ERR wrong number of arguments for 'hset' command
```

| 函数 | 操作 | Redis 3.0 |
|---|---|---|
| `RedisHSetObj` | 多字段 HSET（map 展开） | ❌ 报错，写入失败 |
| `RedisHGetObj` | HGETALL | ✅ 但 key 从未写入，永远 miss |
| `RedisHSetField` | 单字段 HSET | ✅ 有 TTL 守卫，key 不存在时跳过 |
| `RedisHIncrBy` | HINCRBY | ✅ 有 TTL 守卫，key 不存在时跳过 |
| `RedisSet/Get/IncrBy` | 字符串操作 | ✅ 正常 |
| `PING` / 连接建立 | — | ✅ 成功，**不报错，表面正常** |

**最危险之处**：应用启动不报错，日志显示 Redis 已连接，但用户缓存和 Token 缓存永远写不进去。每次 API 请求的 token/user 查询全部穿透到 PostgreSQL，Redis 形同虚设，DB 压力是预期的数倍。

#### Redis 4.0+：兼容，可用

| 检查项 | 结果 | 说明 |
|---|---|---|
| 多字段 HSET | ✅ 4.0 引入 | `RedisHSetObj` 正常工作 |
| `SET/GET/DEL/HGETALL/HINCRBY/EXPIRE/TTL/INCRBY` | ✅ 全部兼容 | Redis 2.x 基础命令 |
| `MULTI/EXEC`（TxPipeline） | ✅ 兼容 | Redis 1.2 引入 |
| Redis 7.0 新命令 | ✅ 未使用 | `LMPOP/ZMPOP/SINTERCARD` 等均未用到 |
| EXPIRE 行为差异 | ✅ 无影响 | 代码始终传正数 TTL，不受 Redis 7.0 负数报错影响 |

#### 版本选择建议

| 版本 | 可用性 | 建议 |
|---|---|---|
| Redis 3.0 | ❌ 不可用 | 核心缓存完全失效 |
| Redis 4.0 | ✅ 最低可用 | 勉强可用，已停止维护 |
| Redis 5.0+ | ✅ 推荐 | 稳定，经过生产验证 |
| Redis 6.0+ | ✅ 最佳 | go-redis v8 的目标版本 |

**Redis 数据无需迁移的原因：**

Redis 中存储的全部是从 PostgreSQL 派生的缓存：

| Key 模式 | 内容 | 说明 |
|---|---|---|
| `user:{id}` | 用户 quota/status/group 缓存 | 有 TTL，miss 自动回源 DB |
| `token:{hmac_key}` | Token 剩余 quota 缓存 | 有 TTL，miss 自动回源 DB |
| `notify_limit:*` | 通知频率限流计数器 | 临时计数，过期消失 |

切换到新 Redis 后缓存为空，应用会自动从 PostgreSQL 重建，不影响业务正确性。

**关于 BatchUpdate 和数据丢失风险：**

`BATCH_UPDATE_ENABLED=true` 时，quota 变化先累积在**进程内存**（`batchUpdateStores` map），每隔 `BATCH_UPDATE_INTERVAL`（默认 5 秒）刷回 PostgreSQL。这部分数据不在 Redis 里，与 Redis 迁移无关。

只要在停服前执行 `docker stop new-api`（Docker 发送 SIGTERM，Go 进程有 10 秒优雅关闭窗口完成最后一次 batch flush），内存中的 pending 数据就会安全落库。

---

## 需要修改的文件

### 必须修改

| 文件 | 修改内容 |
|---|---|
| `docker-compose.yml` | 引入 profiles；连接串改为环境变量占位；移除 `postgres`/`redis` 服务的 `depends_on`；删除 `pg_data` volume；添加 `SESSION_SECRET` |

### 新增文件

| 文件 | 用途 |
|---|---|
| `.env.local` | 测试环境变量（本地容器地址），不提交 git |
| `.env.prod` | 生产环境变量（云服务地址），不提交 git，或直接在服务器配 |

### 确认 `.gitignore`

```gitignore
# 确保这两行存在
.env.local
.env.prod
```

### 不需要修改的文件

- `common/redis.go`：`redis.ParseURL` 支持标准 `redis://` 和 `rediss://`（TLS），云 Redis 主备版直接兼容
- `model/main.go` 及所有 migration 代码：SQL 语法均兼容 PG14
- 所有业务代码：无需改动

---

## 迁移前准备

在不停服的情况下完成以下准备，全部就绪后再执行切换。

### 1. 云服务创建

**PostgreSQL RDS：**

- 版本：14（与当前容器 15 最接近的可用版本）
- 规格：根据现有负载选择，建议先看容器的实际内存/CPU 使用
- 数据库名：`new-api`（与现有一致）
- 创建同名用户，授予该数据库的完整权限

**Redis：**

- 版本：6.0
- 类型：**主备版（标准版）**，不要选集群版
- 确认对外暴露单一主库地址（非 Sentinel 地址）

### 2. 网络连通性确认

在应用服务器上测试：

```bash
# 测试 PostgreSQL 连通性（替换实际地址）
psql -h <RDS_HOST> -U <DB_USER> -d new-api -c "SELECT version();"

# 测试 Redis 连通性
redis-cli -h <REDIS_HOST> -p 6379 -a <REDIS_PASSWORD> PING
```

两者都返回正常响应后再继续。

### 3. 确认安全组 / 白名单

- RDS 安全组：放行应用服务器 IP → 5432 端口
- Redis 白名单：放行应用服务器 IP → 6379 端口
- 若云服务强制 SSL，记录下来（PostgreSQL 连接串需加 `?sslmode=require`，Redis 需用 `rediss://` 协议）

### 4. 备份当前配置

```bash
cp docker-compose.yml docker-compose.yml.bak
docker exec postgres pg_dumpall -U root --globals-only > globals_backup.sql
```

---

## 数据迁移步骤

### PostgreSQL 数据迁移（停服窗口内执行）

#### 步骤 1：停止应用（等待 batch flush）

```bash
# docker stop 会发送 SIGTERM，Go 进程优雅关闭，等待最后一次 batch flush
docker stop new-api
# 等待容器完全停止
docker ps | grep new-api   # 确认不在列表中
```

#### 步骤 2：导出数据

```bash
# 使用 plain 格式（默认），带时间戳备份文件名
docker exec postgres pg_dump \
  -U root \
  --no-owner \
  --no-acl \
  --format=plain \
  new-api > backup_$(date +%Y%m%d_%H%M%S).sql

# 确认文件有内容
wc -l backup_*.sql
```

#### 步骤 3：导入到云 RDS

```bash
# 替换为实际的 RDS 地址和用户名
psql -h <RDS_HOST> -U <DB_USER> -d new-api < backup_*.sql
```

若云 RDS 要求 SSL：

```bash
PGSSLMODE=require psql -h <RDS_HOST> -U <DB_USER> -d new-api < backup_*.sql
```

#### 步骤 4：验证行数

在容器 PostgreSQL 和云 RDS 上分别执行，对比核心表的行数：

```bash
# 容器（导出前的基准）
docker exec postgres psql -U root -d new-api -c "
SELECT 'users' AS tbl, COUNT(*) FROM users
UNION ALL SELECT 'tokens', COUNT(*) FROM tokens
UNION ALL SELECT 'channels', COUNT(*) FROM channels
UNION ALL SELECT 'logs', COUNT(*) FROM logs
UNION ALL SELECT 'top_ups', COUNT(*) FROM top_ups;
"

# 云 RDS
psql -h <RDS_HOST> -U <DB_USER> -d new-api -c "
SELECT 'users' AS tbl, COUNT(*) FROM users
UNION ALL SELECT 'tokens', COUNT(*) FROM tokens
UNION ALL SELECT 'channels', COUNT(*) FROM channels
UNION ALL SELECT 'logs', COUNT(*) FROM logs
UNION ALL SELECT 'top_ups', COUNT(*) FROM top_ups;
"
```

行数完全一致后继续。

### Redis 迁移

**无需操作。** 直接在新配置中指向云 Redis，应用启动后自动重建缓存。

---

## 生产切换操作

完整停服窗口操作流程，预计耗时 **10～20 分钟**。

### 修改 docker-compose.yml

按以下结构修改（核心变化：引入 profiles、改用环境变量、去掉 `version:` 声明以启用 `required: false`）：

```yaml
# docker-compose.yml（修改后）

services:
  new-api:
    image: crpi-xzr81d0490mc3794.cn-shanghai.personal.cr.aliyuncs.com/reputationly/new-api:latest
    container_name: new-api
    restart: always
    command: --log-dir /app/logs
    ports:
      - "127.0.0.1:3000:3000"
    volumes:
      - ./data:/data
      - ./logs:/app/logs
    environment:
      - SQL_DSN=${SQL_DSN}
      - REDIS_CONN_STRING=${REDIS_CONN_STRING}
      - SESSION_SECRET=${SESSION_SECRET}
      - TZ=Asia/Shanghai
      - ERROR_LOG_ENABLED=true
      - BATCH_UPDATE_ENABLED=true
      - NODE_NAME=${NODE_NAME:-new-api-node-1}
      - WATCHTOWER_API_TOKEN=${WATCHTOWER_API_TOKEN}
      - WATCHTOWER_API_URL=http://watchtower:8080
    depends_on:
      postgres:
        condition: service_healthy
        required: false   # profiles 未激活时跳过此依赖
      redis:
        condition: service_healthy
        required: false
    networks:
      - new-api-network
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://localhost:3000/api/status | grep -o '\"success\":\\s*true' || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3

  watchtower:
    image: crpi-xzr81d0490mc3794.cn-shanghai.personal.cr.aliyuncs.com/reputationly/watchtower:latest
    container_name: watchtower
    restart: always
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /root/.docker/config.json:/config.json
    environment:
      - WATCHTOWER_HTTP_API_UPDATE=true
      - WATCHTOWER_HTTP_API_TOKEN=${WATCHTOWER_API_TOKEN}
      - WATCHTOWER_HTTP_API_PERIODIC_POLLS=false
      - WATCHTOWER_CLEANUP=true
      - DOCKER_API_VERSION=1.44
    networks:
      - new-api-network

  postgres:
    profiles: [local]   # 仅测试环境启动
    image: crpi-xzr81d0490mc3794.cn-shanghai.personal.cr.aliyuncs.com/reputationly/postgres:15
    container_name: postgres
    restart: always
    environment:
      POSTGRES_USER: root
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-changeme}
      POSTGRES_DB: new-api
    volumes:
      - pg_data:/var/lib/postgresql/data
    networks:
      - new-api-network
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U root -d new-api"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    profiles: [local]   # 仅测试环境启动
    image: crpi-xzr81d0490mc3794.cn-shanghai.personal.cr.aliyuncs.com/reputationly/redis:latest
    container_name: redis
    restart: always
    command: ["redis-server", "--requirepass", "${REDIS_PASSWORD:-changeme}"]
    networks:
      - new-api-network
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD:-changeme}", "PING"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  pg_data:

networks:
  new-api-network:
    driver: bridge
```

### 生产 `.env.prod` 内容

```env
# PostgreSQL 云 RDS 主库地址
# 若 RDS 强制 SSL，追加 ?sslmode=require
SQL_DSN=postgresql://<DB_USER>:<DB_PASSWORD>@<RDS_HOST>:5432/new-api

# Redis 云主备版主库地址
# 若使用 TLS，协议改为 rediss://
REDIS_CONN_STRING=redis://:<REDIS_PASSWORD>@<REDIS_HOST>:6379

# 必填：多节点/重启后 session 保持一致性
SESSION_SECRET=<32位以上随机字符串>

# Watchtower
WATCHTOWER_API_TOKEN=<随机令牌>

# 节点名（多节点时各节点填不同值）
NODE_NAME=new-api-node-1
```

### 测试 `.env.local` 内容

```env
SQL_DSN=postgresql://root:changeme@postgres:5432/new-api
REDIS_CONN_STRING=redis://:changeme@redis:6379
SESSION_SECRET=dev-session-secret
WATCHTOWER_API_TOKEN=dev-token
POSTGRES_PASSWORD=changeme
REDIS_PASSWORD=changeme
NODE_NAME=new-api-dev
```

### 切换命令（生产）

```bash
# 数据迁移完成后，启动指向云服务的新配置
docker-compose --env-file .env.prod up -d

# 观察启动日志，确认数据库连接成功
docker logs -f new-api --tail 50
```

### 测试环境启动命令

```bash
# 带 local profile，启动本地 postgres 和 redis 容器
docker-compose --profile local --env-file .env.local up -d
```

---

## 降级后注意事项

### PG15 → PG14 降级专项

#### 1. GORM migration 在 PG14 上会重新执行

应用首次连接云 RDS 时，`migrateDB()` 会对所有表运行 AutoMigrate 和自定义 migration（`ALTER COLUMN TYPE`、`ALTER COLUMN ... USING` 等）。这些语句幂等，重复执行无害，但需要：

- **数据库用户具有 `ALTER TABLE` 权限**。阿里云 RDS 的普通用户默认不是 superuser，需手动授权：
  ```sql
  GRANT ALL PRIVILEGES ON DATABASE new-api TO <DB_USER>;
  -- 如果表已存在，还需要：
  GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO <DB_USER>;
  GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO <DB_USER>;
  ```

#### 2. `pg_dump` 版本注释不影响数据，但要留意

dump 文件头部会有：
```sql
-- Dumped from database version 15.x
-- Dumped by pg_dump version 15.x
```

这只是注释，不影响 `psql` 执行。但如果 dump 中有任何 PG15 专属语法而未被发现，PG14 会在 restore 阶段报错。此时需要：
1. 检查报错的具体 SQL 语句
2. 手动调整该语句为 PG14 兼容写法
3. 重新执行 restore

#### 3. 时区与 `timestamptz` 行为

PG14 和 PG15 对 `timestamptz` 的处理一致，但要确认云 RDS 的 `TimeZone` 参数与容器一致（容器通过 `TZ=Asia/Shanghai` 设置）。在云 RDS 控制台确认参数组中 `TimeZone=Asia/Shanghai`，或在连接串中指定：

```
SQL_DSN=postgresql://...@host:5432/new-api?TimeZone=Asia%2FShanghai
```

#### 4. 连接数限制

容器 PG 默认 `max_connections=100`。云 RDS 根据规格设置，小规格实例可能更低（如 60）。应用配置了：

```
SQL_MAX_IDLE_CONNS=100
SQL_MAX_OPEN_CONNS=1000
```

需要确认云 RDS 的 `max_connections` 大于 `SQL_MAX_OPEN_CONNS`，否则会出现 `too many connections` 错误。建议将 `SQL_MAX_OPEN_CONNS` 设置为不超过 RDS `max_connections` 的 80%。

### Redis 6.0 降级专项

#### 1. 缓存冷启动期的性能影响

切换到新 Redis 后，所有缓存为空。在流量恢复的最初几分钟内，每个 token/user 请求都会穿透到 PostgreSQL 查询（cache miss）。表现为：

- 首个请求的响应延迟略高（新增一次 DB 查询）
- PostgreSQL 连接数短暂升高

这是正常现象，缓存会在几分钟内自动预热，不需要干预。如果流量很大，建议在低峰期切换。

#### 2. 确认云 Redis 不是 Sentinel/Cluster 模式

代码使用 `redis.ParseURL(url)` 返回 `*redis.Client`（单节点客户端），不支持 Sentinel 和 Cluster。

阿里云 Redis 主备版对外暴露的是**单一主库地址**，兼容；集群版（Cluster）对外暴露的是集群代理地址，**不兼容，不要选**。

连接串格式：
```
# 标准（无 TLS）
redis://:<password>@r-xxxx.redis.rds.aliyuncs.com:6379

# TLS（云服务开启加密传输时）
rediss://:<password>@r-xxxx.redis.rds.aliyuncs.com:6379
```

#### 3. SESSION_SECRET 必须设置

切换后 `SESSION_SECRET` 若未设置，每次重启容器都会生成新的随机 secret，导致所有已登录用户的 session 失效（需重新登录）。`.env.prod` 中必须固定这个值。

---

## 回滚方案

如果切换后发现问题，回滚到容器方案：

```bash
# 1. 停止当前容器
docker-compose --env-file .env.prod down

# 2. 用备份的 docker-compose 重新启动
docker-compose -f docker-compose.yml.bak up -d

# 3. 如果 pg_data volume 已删除，需从备份恢复
# 创建新容器，将 backup_*.sql 导入
docker-compose -f docker-compose.yml.bak up -d postgres redis
docker exec -i postgres psql -U root -d new-api < backup_*.sql
docker-compose -f docker-compose.yml.bak up -d new-api watchtower
```

**回滚前提：**
- `docker-compose.yml.bak` 已备份
- `backup_*.sql` 文件保留完整（建议保留至少 7 天）
- 回滚后数据只到停服时刻，切换期间若云 RDS 有写入则会丢失（主备切换后不建议回写容器）

---

## 验证清单

切换完成后逐项确认：

```
□ docker logs new-api 无 ERROR 级别的数据库连接错误
□ curl http://localhost:3000/api/status 返回 "success":true
□ 登录管理后台，确认用户数据、渠道配置正常显示
□ 发起一个 API 请求，确认计费和 quota 扣减正常
□ 检查 PostgreSQL 连接数在 RDS 控制台监控中正常（未超过 max_connections）
□ 检查 Redis 命中率在云控制台监控中逐步上升（缓存预热中）
□ 确认旧的 postgres/redis 容器已停止（不再占用资源）
□ 备份文件 backup_*.sql 已转移到安全位置保存
```

---

## 华为云 RDS 实操补充（2026-04-26）

以下是实际迁移到华为云 RDS PostgreSQL 14 时遇到的问题和注意事项，对阿里云不一定适用。

### 1. 数据库名不能含中划线

华为云控制台「创建数据库」只允许字母、数字、下划线。`new-api` 不合法，需改为 `new_api`。

**影响**：`SQL_DSN` 中数据库名对应修改：

```
SQL_DSN=postgresql://root:<PASSWORD>@<RDS_HOST>:5432/new_api?sslmode=require
```

GORM AutoMigrate 和所有业务代码不受影响，数据库名只体现在连接串里。

### 2. SSL 强制开启

华为云 RDS 默认 `ssl=on`，连接串必须加 `?sslmode=require`，否则连接失败。

```
# 正确
SQL_DSN=postgresql://root:<PASSWORD>@<RDS_HOST>:5432/new_api?sslmode=require

# 错误（连接被拒）
SQL_DSN=postgresql://root:<PASSWORD>@<RDS_HOST>:5432/new_api
```

### 3. 应用服务器默认没有 psql 客户端

需要手动安装：

```bash
apt install -y postgresql-client
```

### 4. 新版服务器没有 `docker-compose`，用 `docker compose`

```bash
# 旧命令（报错）
docker-compose --env-file .env.prod up -d

# 正确命令
docker compose --env-file .env.prod up -d
```

### 5. 密码特殊字符注意事项

密码嵌入 URL 时，`@` 必须编码为 `%40`，`+` 无需编码可直接使用。建议生成密码时避免使用 `@` 字符。

### 6. 华为云 RDS 参数实测值（无需调整）

| 参数 | 值 | 说明 |
|---|---|---|
| `max_connections` | 3072 | 远超默认 `SQL_MAX_OPEN_CONNS=1000`，无需调整 |
| `password_encryption` | scram-sha-256 | 项目用 `pgx/v5`，原生支持，无兼容问题 |
| `timezone` | Etc/GMT-8 | 等价于 Asia/Shanghai，无需额外配置 |
| `idle_session_timeout` | 3600000ms | GORM 连接池自动重连，无影响 |

### 7. 权限说明

华为云 RDS 管理员账户（`root`）直接拥有完整权限，无需额外执行 `GRANT` 语句。若创建独立应用账户则需要手动授权。

---

## 当前架构状态与后续注意事项（2026-04-26）

### 当前生产架构

| 组件 | 方案 | 高可用性 |
|---|---|---|
| PostgreSQL | 华为云 RDS 主备版（PG 14） | ✅ 云平台自动故障切换 |
| Redis | 本机单容器 | ⚠️ 无 HA，容器或宿主机故障则缓存丢失 |
| 应用 | BMS 裸金属单节点 Docker 容器 | — |

### Redis 单容器的风险与接受理由

**风险：**
- Redis 容器崩溃或宿主机重启后，所有缓存数据清空
- 重启后数分钟内所有 token/user 查询穿透到 PostgreSQL（缓存冷启动）
- DB 连接数短暂升高，直到缓存重建完毕（通常 2～5 分钟内恢复正常）

**接受理由：**
- Redis 存储的全部是从 PostgreSQL 派生的缓存（用户 quota、token 信息），TTL 到期自动重建，**不存在数据丢失**
- 业务不中断，只是短暂性能下降
- 华为云 DCS 可用版本（3.0）与代码不兼容（多字段 HSET 需 Redis 4.0+，见兼容性分析章节）
- 自建 ECS + Keepalived HA Redis 的运维复杂度超出当前规模收益

### Redis 后续注意事项

**日常运维：**
- `docker restart redis` 后无需任何操作，应用自动重建缓存
- 重启后观察 `docker logs new-api` 若出现大量 DB 查询属正常现象，等待缓存预热即可

**监控建议：**
- 关注 RDS 控制台连接数监控，Redis 重启后若连接数异常升高不回落，排查是否有慢查询

**若未来需要升级 Redis HA，可选方案：**

| 方案 | 条件 | 改代码 |
|---|---|---|
| 华为云 DCS 主备版 | 等待 DCS 开放 Redis 6.0+ 规格 | 不需要 |
| ECS 自建 + Keepalived VIP | 两台 ECS 同子网，VIP 自动漂移 | 不需要 |
| Redis Sentinel | 3 个 Sentinel 进程 | 需改 `common/redis.go` |

详细方案分析见 `docs/production-ha-migration.md` 兼容性分析章节及 ELB 架构文档。

### PostgreSQL RDS 后续注意事项

**主备切换行为：**
- 华为云 RDS 主备切换时，数据库 DNS 地址不变，TCP 连接会断开
- GORM 连接池会自动重连，应用短暂报数据库连接错误（通常 30 秒内恢复）
- `BATCH_UPDATE_ENABLED=true` 时，切换瞬间内存中未刷库的 quota 增量会随重连安全落库（Go 进程未重启）

**RDS 计划维护窗口：**
- 当前设置为每天 02:00—06:00（GMT+08:00）
- 维护期间可能发生主备切换，建议将低峰流量安排在此窗口外

**连接数管理：**
- RDS `max_connections = 3072`，应用默认 `SQL_MAX_OPEN_CONNS = 1000`，余量充足
- 若扩展到多节点部署，每个节点最多 1000 连接，3 节点以内不超上限
