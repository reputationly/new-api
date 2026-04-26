# 启动指南

不同场景下如何启动 new-api，适合团队成员快速上手。

---

## 场景总览

| 场景 | 数据库 | Redis | 启动方式 |
|---|---|---|---|
| 本地开发（Mac / Windows） | SQLite 文件 | 不需要 | `go run main.go` |
| 测试环境 | 容器 PostgreSQL | 容器 Redis | `docker compose --profile local` |
| 生产环境 | 华为云 RDS 主备 | 容器 Redis | `docker compose --env-file .env.prod` |

---

## 本地开发 — Mac

> 详细工具安装说明见 [DEV_GUIDE.md](./DEV_GUIDE.md)

**前置要求：** Go 1.22+、Bun（前端）

```bash
# 1. 克隆并进入项目
git clone <仓库地址>
cd new-api

# 2. 构建前端（首次或前端有改动时执行）
cd web && bun install && bun run build && cd ..

# 3. 启动后端（SQLite 自动创建，无需配置数据库）
go run main.go
```

访问 `http://localhost:3000`，首次启动会引导创建管理员账号。

**环境变量（可选）：**

```bash
# 不需要任何配置即可运行。如需覆盖默认值，创建 .env 文件：
cp .env.example .env
# 编辑 .env，按需取消注释并修改
```

`.env` 已在 `.gitignore` 中，不会提交到仓库。

---

## 本地开发 — Windows 11

SQLite 驱动使用纯 Go 实现（`modernc.org/sqlite`），**无需 GCC / MinGW，Windows 原生支持**。

**前置要求：**

1. 安装 Go：前往 [https://go.dev/dl](https://go.dev/dl) 下载 `.msi` 安装包
2. 安装 Bun：`powershell -c "irm bun.sh/install.ps1 | iex"`
3. 安装 Git：[https://git-scm.com/download/win](https://git-scm.com/download/win)

**启动步骤：**

```powershell
# 1. 克隆并进入项目
git clone <仓库地址>
cd new-api

# 2. 构建前端（首次或前端有改动时执行）
cd web
bun install
bun run build
cd ..

# 3. 启动后端
go run main.go
```

访问 `http://localhost:3000`。

**环境变量（可选）：**

在项目根目录新建 `.env` 文件（参考 `.env.example`），Windows 下 `go run main.go` 会自动读取。

> **注意：** 如果安装了 Docker Desktop for Windows，也可以用「测试环境」的方式启动，避免在 Windows 上安装 Go。

---

## 测试环境 — 容器 PostgreSQL + Redis

适合：需要与生产数据库类型一致的测试，或不想在本机安装 Go。

**前置要求：** Docker（Mac 用 Docker Desktop，Linux 用 Docker Engine）

```bash
# 1. 准备环境变量文件
cp .env.local.example .env.local
# .env.local 使用默认的 changeme 密码，测试环境够用，无需修改

# 2. 启动（postgres + redis 容器通过 --profile local 激活）
docker compose --profile local --env-file .env.local up -d

# 3. 查看日志确认启动成功
docker logs new-api --tail 30
```

访问 `http://localhost:3000`。

**停止：**

```bash
docker compose --profile local --env-file .env.local down
```

**重置数据（清空数据库重来）：**

```bash
docker compose --profile local --env-file .env.local down -v
# -v 参数会同时删除 pg_data 卷，下次启动是全新数据库
```

---

## 生产环境 — 华为云 RDS + 容器 Redis

仅在生产服务器上操作，`.env.prod` 已在服务器 `/opt/new-api/` 目录下配置好。

```bash
cd /opt/new-api

# 启动（postgres 容器因 profiles: [local] 不会启动）
docker compose --env-file .env.prod up -d

# 查看日志确认数据库连接正常
docker logs new-api --tail 30

# 验证服务可用
curl -s http://localhost:3000/api/status | grep -o '"success":\s*true'
```

**WAF / 反向代理部署时的额外配置（`.env.prod`）：**

| 变量 | 说明 | 示例值 |
|---|---|---|
| `BIND_HOST` | 端口绑定地址。WAF 回源需设为 `0.0.0.0`，直连部署留空（默认 `127.0.0.1`） | `0.0.0.0` |
| `TRUSTED_PROXY_CIDR` | WAF/Nginx 所在子网，Gin 从该段的 `X-Forwarded-For` 取真实 IP。直连部署留空 | `10.0.0.0/24` |
| `STREAMING_TIMEOUT` | Streaming 无响应超时（秒）。接入 DeepSeek R1 等推理模型时建议设为 `300`，默认 `120` | `300` |

**在线升级（通过 Watchtower）：**

```bash
# 触发拉取最新镜像并重启
curl -H "Authorization: Bearer <WATCHTOWER_API_TOKEN>" http://localhost:8080/v1/update
```

**重启应用（改了 .env.prod 后）：**

```bash
docker compose --env-file .env.prod up -d new-api
```

---

## 常见问题

**Q: `go build` 报错 `pattern web/dist: no matching files found`**

需要先构建前端：

```bash
cd web && bun install && bun run build && cd ..
```

**Q: 本地启动后访问 3000 端口页面空白**

前端没有构建。执行上面的前端构建命令后重新 `go run main.go`。

**Q: 测试环境 postgres 容器起不来**

检查 `.env.local` 是否存在：

```bash
ls .env.local   # 不存在则 cp .env.local.example .env.local
```

**Q: 生产环境日志报数据库连接错误**

检查 `.env.prod` 中 `SQL_DSN` 的地址和密码，以及华为云 RDS 安全组是否放行了服务器 IP → 5432 端口。

**Q: 限流/审计日志里看到的 IP 是反向代理的 IP 而非用户真实 IP**

未配置 `TRUSTED_PROXY_CIDR`，Gin 不信任代理转发的 `X-Forwarded-For`。在 `.env.prod` 中设置 WAF/Nginx 所在子网 CIDR，例如 `TRUSTED_PROXY_CIDR=10.0.0.0/24`。

**Q: 首次登录的管理员账号是什么**

首次访问 `http://localhost:3000`，系统会引导创建初始管理员账号，没有预设账号。
