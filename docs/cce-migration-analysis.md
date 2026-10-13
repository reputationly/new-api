# new-api 从 BMS 单容器迁移到 CCE：改造分析

> 依据：new-api main（1fe627469）代码、现网 compose、《云容器引擎 用户指南》（2025-04-30，下文页码为手册印刷页码）。
> 本文只做分析，不含代码改动。v2：补充 WAF/ELB 现状、批量写、长连接、自动迁移、HA、日志、插件、集群选型（1.34 / containerd / x86）、连接数。标「推断」的地方还没有实测，标「待确认」的需要你提供现网信息。

## 1. 结论

迁到 CCE 的改造量集中在 4 处，其余都是配置和运维层面的工作：

| 优先级 | 改造项 | 是否改代码 |
|---|---|---|
| 必须 | ① 在线升级（Watchtower）下线，改用 CCE 滚动升级和回退 | 改（后端接口 + 前端按钮） |
| 必须 | ② 主节点（NODE_TYPE）：多副本默认全部是主节点，会并发跑数据库迁移和全部定时任务 | 部署层面可解决；2 个定时任务要补主节点判断 |
| 必须 | ③ 优雅退出：进程不处理 SIGTERM，滚动升级时会断掉在途请求，丢失未落库的额度变动 | 改 |
| 必须 | ④ 镜像仓库：现在用阿里云 ACR，CCE 应改用华为云 SWR | 改 CI workflow |
| 必须 | 配置和密钥：.env.prod 拆成 ConfigMap 和 Secret | 不改 |
| 必须 | 本地文件：日志、/data/wellknown、NFS 输入盘改为 CCE 存储 | 不改 |
| 必须 | 网络：入口（复用 WAF + ELB）、真实 IP、出公网（NAT）、RDS/DCS 安全组 | 不改 |
| 必须 | 数据库 / Redis 连接数：每个 Pod 默认最多 1000 个数据库连接，多副本相乘可能超过 RDS 上限 | 不改（配环境变量） |
| 建议 | 健康检查：区分 startup、readiness、liveness 探针 | 可不改 |
| 可选 | Redis 选主（代替手工拆分主从）、HPA 弹性伸缩 | 改 |

你的判断是对的：页面上的「在线升级」在 CCE 上不能用，要改由 CCE 托管升级和回滚。但这只是改造量最小的一项。真正的风险在第 ② 和第 ③ 项：多副本后，「谁是主节点」和「Pod 被删时会发生什么」这两件事，现在的代码都是按单容器写的。

---

## 2. 现状（BMS 单容器）

- `/opt/new-api/docker-compose.yml`：new-api 和 watchtower 两个服务，`docker compose --env-file .env.prod`。
- new-api 镜像：`crpi-xzr81d0490mc3794.cn-shanghai.personal.cr.aliyuncs.com/reputationly/new-api:latest`（阿里云 ACR 上海个人版）。
- 入口：用户 → WAF → ELB → BMS 上 new-api 的 3000 端口（已确认）。
- 挂载：`./data:/data`（WORKDIR，含 `/data/wellknown` 域名验证文件），`./logs:/app/logs`（`--log-dir /app/logs` 文件日志）。
- 外部依赖：华为云 RDS PostgreSQL 14、DCS Redis 3.0 主备，这两项已经在 BMS 之外，CCE 上直接复用。
- 升级：后台「设置 → 其他设置 → 检查更新 → 立即安装」，调 `POST /api/system/update`，由 watchtower 拉 `:latest` 重建容器。

---

## 3. 逐项分析

### ① 在线升级 → CCE 滚动升级 / 回退（必须，改代码）

**现在的实现**
- 后端 `controller/system.go` 的 `PerformUpdate`：请求 `WATCHTOWER_API_URL/v1/update`。
- 前端 `web/classic/src/components/settings/OtherSetting.jsx`：`checkUpdate` 查 GitHub `reputationly/new-api` 的最新 release，`handleUpdate` 调上面的接口。
- watchtower 需要挂载宿主机的 `/var/run/docker.sock`。

**为什么在 CCE 上不能用**
- CCE 节点用的是 containerd，Pod 里不该挂 docker.sock。即使挂上，watchtower 绕过 Deployment 直接重建容器，也会被 Kubernetes 再拉回 Deployment 里声明的旧镜像。
- 镜像 tag 用 `:latest`，回退时没有版本可选。

**改造方案**
- 后端：在 CCE 部署下关掉 `/api/system/update`。最简单的做法是不配 `WATCHTOWER_API_*`，接口已经会返回「升级服务未配置」；更彻底的做法是删掉这个接口。
- 前端：「检查更新」可以保留，用来显示有新版本；「立即安装」按钮在未配置 Watchtower 时隐藏，或改成提示「请在 CCE 控制台升级」。
- 发布流程改为：
  1. 打 tag 出包；
  2. CI 推送**不可变 tag** 的镜像到 SWR，例如 `v0.13.2-ovaijisuan-20261013-xxxxxxxx`，不再依赖 `:latest`；
  3. 在 CCE 控制台「升级」里换镜像版本，或执行 `kubectl set image`。
- 回退：手册 8.6（p383）说明「回退」功能只支持无状态工作负载。可回退的版本数由 `revisionHistoryLimit` 决定，默认保留 10 个 ReplicaSet（p364）。
- 滚动参数（p363–364）：建议 `maxSurge: 1, maxUnavailable: 0`，先起新 Pod、就绪后再删旧 Pod；`minReadySeconds` 设 10–30 秒，防止新 Pod 一起来就被判定可用。

**回退的限制**（推断，要在发布规范里写清楚）：CCE 回退只回退镜像，不回退数据库。GORM AutoMigrate 只做加表、加列，旧版本通常能忽略新列，可以回退。但像额度单位 V2 这种数据迁移是单向的，跨过这类版本就不能靠 CCE 回退，必须走单独的回滚预案。

### ② 主节点（NODE_TYPE）与多副本（必须）

**现在的逻辑**：`common/init.go` 里 `IsMasterNode = os.Getenv("NODE_TYPE") != "slave"`，也就是说**不设置就是主节点**。以下逻辑只在主节点上执行：

| 位置 | 内容 |
|---|---|
| `model/main.go` | 数据库迁移（主库 AutoMigrate、日志库迁移） |
| `main.go` | 异步任务轮询 `UpdateTaskBulk` / `UpdateMidjourneyTaskBulk`；审核日志和取证对象清理 |
| `controller/channel-test.go` | 渠道自动测试 |
| `controller/channel_upstream_update.go` | 渠道上游模型更新检查 |
| `service/subscription_reset_task.go` | 订阅额度重置 |
| `service/codex_credential_refresh_task.go` | Codex 凭证刷新 |
| `router/main.go` | 前端重定向（FRONTEND_BASE_URL） |

**问题**：Deployment 的每个副本用的是同一份环境变量。直接扩到 N 个副本，就有 N 个主节点：
- N 个副本同时跑 AutoMigrate，滚动升级时新旧版本可能同时在改表结构；
- 任务轮询会重复 N 次。任务落终态有 CAS 保护（`model/task.go` 的 `UpdateWithStatus`），所以不会重复结算，但上游查询量会变成 N 倍；
- 渠道自动测试、订阅重置等任务也会重复执行。订阅重置是否幂等**待核实**。

**还有两个定时任务没有加主节点判断**，即使拆分了主从，也会在每个副本上都执行：
- `service/cron_direct_payment.go` `StartDirectPaymentExpiryScan`：支付订单过期扫描，多副本会重复扫描（推断：会把订单置为过期，需要确认是否幂等）；
- `service/cron_media_storage.go` `StartMediaStorageStatsTask`：OBS 桶用量快照和阈值告警，多副本会**重复写快照、重复发告警**。

→ 这两处要加 `common.IsMasterNode` 判断，改动很小。

**方案 A（推荐先用，基本不改代码）**：拆成两个 Deployment，挂在同一个 Service 后面，一起接流量：
- `new-api-master`：1 副本，不设 NODE_TYPE，负责迁移和定时任务。升级策略用 `maxSurge: 0, maxUnavailable: 1`，保证任何时刻最多只有 1 个主节点。代价是它升级时会少一个副本，由 slave 承接流量。
- `new-api-slave`：N 副本，设 `NODE_TYPE=slave`，只处理请求。
- 发布顺序：**先升级 master，等迁移完成、Pod 就绪后再升级 slave**。否则新版 slave 可能在表结构变更前就开始读新列。这个顺序要写进发布流程。
- 局限：master 挂掉到被重新拉起的这段时间里，定时任务会停（通常是秒级到分钟级）。

**升级时自动改数据库仍然支持**：迁移是 new-api 启动时自己跑的（`model/main.go` InitDB → AutoMigrate），和 watchtower 无关，CCE 上 master Pod 启动时照样会跑。滚动升级时新旧版本会同时运行一小段时间：加表、加列没问题，旧版本会忽略新列；删列、改列名这类不兼容迁移会让旧 Pod 出错，遇到这种版本要停机发布，或拆成两次发版（先兼容、再清理）。

**方案 B（以后再做，要改代码）**：用 Redis 锁选主（SET NX PX + 续期，Redis 3.0 支持），所有副本用同一份配置，谁拿到锁谁执行定时任务。数据库迁移仍然要保证只跑一次，可以改成 CCE 的 Job，或者在启动时用数据库锁。

如果第一阶段只跑 1 个副本，② 可以暂缓，但 ① ③ ④ 都必须做。

### ③ 优雅退出（必须，改代码）

**现状**：`main.go` 最后是 `server.Run(":"+port)`，代码里没有 `signal.Notify` 或 `Shutdown`。手册 p364 说明，删除 Pod 时先发 SIGTERM，超过 `terminationGracePeriodSeconds`（默认 30 秒）后再发 SIGKILL。Go 进程收到 SIGTERM 默认直接退出，所以：

- **在途请求被切断**：SSE 流式、WebSocket realtime、长耗时同步请求都会断，用户看到报错。计费按流式中断处理，具体影响**待核实**。
- **额度批量更新丢失**：现网 `BATCH_UPDATE_ENABLED=true`，`model/utils.go` 把用户、令牌、渠道的额度变动在内存里累加，每 5 秒（BATCH_UPDATE_INTERVAL）刷一次库。进程被杀时，最近一个周期的变动会丢失，额度账就对不上了。
- **审核日志丢失**：`model/moderation_log.go` 的异步队列里没落库的记录会丢。

BMS 上现在也有这些问题，只是只在升级时发生一次。到了 CCE，滚动升级、节点排水、缩容、Pod 漂移都会触发，频率高得多。

**改造方案**
1. 代码：改用 `http.Server`，收到 SIGTERM 后执行：
   - 先让 readiness 返回失败，摘掉流量；
   - 调用 `Shutdown(ctx)`，等在途请求结束；
   - 执行一次 `batchUpdate()` 把内存里的额度变动刷库；
   - 关闭审核日志队列，排空后退出。
   - WebSocket（realtime 语音）要单独处理：Go 的 `http.Server.Shutdown` 不会等待已升级的连接（WebSocket），要自己跟踪这些连接，等它们结束或超时再退出。
2. Pod 配置：
   - `preStop` 先 sleep 5–10 秒，等 ELB/Service 摘除这个后端，避免关闭后还有新请求进来（手册 8.3.5 p355「停止前处理」）；
   - `terminationGracePeriodSeconds` 要大于最长的同步请求时间。建议按最长的单次输出时长设，例如推理模型可能输出几分钟，就设 5–10 分钟。注意 `STREAMING_TIMEOUT=120` 是「120 秒没有新输出就超时」，不是请求总时长上限，不能拿它当宽限期的依据。代价是升级变慢：旧 Pod 要等最长的请求结束才退出。

做完以后，滚动升级对用户基本无感：正在输出的流继续连在旧 Pod 上直到结束，新请求只进新 Pod。超过宽限期的超长输出还是会被切断。视频等异步任务状态存在数据库里，不受 Pod 重启影响。

### ④ 镜像仓库（必须，改 CI）

- 现在镜像在阿里云 ACR（上海）。CCE 拉第三方仓库的镜像，需要两样东西：一是镜像访问凭证（手册 p322「使用第三方镜像」，即 imagePullSecret）；二是节点能访问公网（NAT 或 EIP）。每次扩容、Pod 漂移都要跨云拉镜像，速度和可用性都依赖公网。
- 建议改用华为云 SWR（同区域、内网拉取）。`docker-image-ovaijisuan.yml` 增加推送 SWR 的步骤，阿里云 ACR 推送是否保留由你决定。

### ⑤ 配置与密钥（必须，不改代码）

- `.env.prod` 拆成两部分：
  - ConfigMap：TZ、BATCH_UPDATE_ENABLED、STREAMING_TIMEOUT、TRUSTED_PROXY_CIDR、ERROR_LOG_ENABLED 等；
  - Secret：SQL_DSN、REDIS_CONN_STRING、SESSION_SECRET、CRYPTO_SECRET、KYC_ENCRYPT_KEY、KYC_HASH_KEY、MODERATION_ENCRYPT_KEY、OBS_AK/OBS_SK。
- **所有副本的 SESSION_SECRET 必须相同**：session 用 cookie 存储（`main.go` 的 `cookie.NewStore`），密钥不同，用户的请求落到另一个副本就会掉登录。CRYPTO_SECRET 没配时取 SESSION_SECRET，用于签分享链接，同样必须所有副本一致且固定不变。KYC 和审核加密 key 也必须和现网完全一致，否则历史数据解不开。
- `NODE_NAME` 用 downward API 取 `metadata.name`，审计日志里能区分是哪个 Pod 写的。
- 去掉 `WATCHTOWER_API_TOKEN`、`WATCHTOWER_API_URL`。
- 真实值不进 Git，仓库只放占位符模板；集群外另留一份安全备份。改 ConfigMap/Secret 后要「重新部署」（滚动重启）才生效。
- **CCE 回退不会回退 ConfigMap 和 Secret**，只回退镜像和 Deployment 本身。改配置前先导出旧 YAML 备份，出问题手动改回。以后可以用 DEW 插件托管密钥（审计、轮换）。

### ⑥ 本地文件与存储（必须，不改代码）

| 现在 | 用途 | CCE 上 |
|---|---|---|
| `--log-dir /app/logs`（`logger/logger.go` 写文件） | 运行日志 | 去掉 `--log-dir`，只输出到 stdout，用 CCE 的日志采集接入 LTS。也可以保留文件日志、挂 emptyDir，但 Pod 删除后文件就没了 |
| `/data/wellknown`（`router/wellknown-router.go`，启动时读入） | 域名验证文件 | 做成 ConfigMap 挂到 `/data/wellknown`，或者用 `WELL_KNOWN_DIR` 指到别的目录 |
| `/data` 下的其他内容 | **待确认**（`ls -la /opt/new-api/data`） | 用 SQLite 才需要持久化。现网用 RDS，推断可以用 emptyDir |
| `os.TempDir()/new-api-body-cache`、OBS 上传临时文件、Dify 上传临时文件 | 临时文件 | emptyDir（可以设 sizeLimit） |
| LightX2V NFS 输入盘（`nfsinput.ProbeNFSInputs`，启用后探测失败直接 Fatal） | 和 gpustack 共享 SFS | 用 SFS / SFS Turbo 静态 PV（手册 11.4，p806 起），**挂载路径必须和 gpustack 的 `lightx2v_output_root` 是同一个绝对路径**。现网有没有启用这个功能**待确认** |
| ffmpeg（视频审核抽帧） | 已经打进镜像 | 不用改 |

### ⑦ 网络（必须，不改代码）

- **入口**：WAF 不动，ELB 后端从 BMS 改成 CCE 的 ELB 型 Service 或 ELB Ingress（手册 10.3.4 / 10.4.3），可以复用现有 ELB。HTTPS 证书在哪一层（WAF 还是 ELB）保持不变。
- **集群是 CCE Standard（已确认，当前只有这个版本）**：流量路径是 ELB → 节点 → Pod。手册 10.3.4.10（p600）的「ELB 健康检查通过后 Pod 才就绪」（readinessGates）只支持 Turbo + 独享型 ELB 直通，**Standard 用不了**。升级不中断改靠：readiness 探针、`minReadySeconds`、preStop 多等 10–15 秒（让 kube-proxy 和 ELB 都摘掉旧 Pod），再加第 ③ 项的优雅退出。慢启动（10.4.3.4.7 p671，新 Pod 线性加流量）Standard 和 Turbo 都支持（v1.23+），前提是**独享型 ELB**。现有 ELB 是共享型还是独享型**待确认**。SSE 和长请求要调大 ELB 的超时时间（Service 见 10.3.4.6 p589，Ingress 见 10.4.3.4.6 p667），并确认 WebSocket（realtime）能穿过。
- **真实 IP**：IP 限流和日志里的 IP 依赖 `TRUSTED_PROXY_CIDR`（`main.go` 的 `SetTrustedProxies`）。要设成 ELB / Ingress 转发过来的源网段，否则所有请求都会被识别成同一个 IP，按 IP 的限流会误伤所有用户。
- **访问 RDS 和 DCS**：手册 p768 的排查说明里写了，对端安全组要按集群的网络模型放通：容器隧道网络放通节点 IP，VPC 网络放通容器网段，云原生 2.0 放通容器子网。现在 sg-newapi 只放了 `10.0.0.0/24`，如果容器网段不在这个范围内，要补规则。
- **出公网**：调用上游 AI 厂商、OBS 公网域名、支付回调、GitHub 检查更新，都要通过 NAT 网关 SNAT 或节点 EIP 出公网（手册 10.8 p768）。**出口 IP 会变**。如果有上游厂商或支付渠道配置了 BMS 出口 IP 白名单，要提前加上新的出口 IP（**待确认**）。

### ⑧ 多副本下的进程内状态（了解即可，不用改）

- **额度批量写在多副本下可以继续用，不用改成单次写**：`model/utils.go` 每个副本在内存里累加，每 5 秒写库一次，写库用增量 SQL（`quota = quota + ?`，见 `increaseUserQuota`、`increaseTokenQuota`、`updateChannelUsedQuota`、`updateUserUsedQuotaAndRequestCount`），多个副本的增量由数据库原子相加，不会互相覆盖。唯一风险是 Pod 被杀时丢最近 5 秒，由第 ③ 项的优雅退出解决。

- 设置项、渠道缓存、渠道成本、模型可见性：每个副本每 `SYNC_FREQUENCY`（默认 60 秒）从数据库同步一次。后台改完设置后，其它副本最多 60 秒才生效；如果嫌慢，可以把 SYNC_FREQUENCY 调小。
- 限流：Redis 可用时走 Redis（`middleware/rate-limit.go`、`model-rate-limit.go`），多副本共享，不用改。
- 数据看板 `UpdateQuotaData`：每个副本只聚合自己处理过的请求，再各自写库，不会重复计数（推断：按 key 累加写入，上线前最好核对一次）。

### ⑨ 健康检查与资源（建议）

- 现在的健康检查是请求 `/api/status` 并匹配 `success:true`。这个接口返回整份状态 JSON，比较重。
  - startupProbe：用 `/api/status`，阈值放宽。启动时要跑数据库迁移（`model/main.go` 的 InitDB）和初始化渠道缓存；
  - readinessProbe：用 `/api/status`；
  - livenessProbe：用 TCP 3000，或者以后新增一个轻量的 `/healthz`，避免数据库抖一下就把容器重启。
- 资源：设置 requests 和 limits。镜像用 Go 1.26 构建，Go 1.25 起 GOMAXPROCS 会按容器的 CPU limit 自动设置（推断，按 Go 版本说明），不需要另外配置。
- 反亲和：slave 设 podAntiAffinity，把副本分散到不同节点（手册 8.4.4 p375）。
- HPA（手册 12.2）可以第二阶段再考虑。

### ⑩ 数据库 / Redis 连接数（必须，配环境变量）

- `model/main.go`：每个 Pod 默认 `SQL_MAX_OPEN_CONNS=1000`、`SQL_MAX_IDLE_CONNS=100`。现在单容器问题不大，多副本后上限要相乘，例如 5 个副本就是 5000，可能超过 RDS 的 `max_connections`，高峰时新连接会被拒。上线前查 RDS 的 `max_connections`（**待确认**），按「副本数 × 每 Pod 上限 < max_connections × 80%」给每个 Pod 显式设 `SQL_MAX_OPEN_CONNS`，比如 100–200。
- `common/redis.go`：每个 Pod `REDIS_POOL_SIZE` 默认 10，和现在单容器一样。多副本后总连接数增加，但远低于 DCS 上限，不用调。

### ⑪ 高可用（HA）

CCE 自带的故障处理，不需要人工迁移业务：
- 容器挂了：按 liveness 探针在原节点自动重启。
- Pod 不健康：readiness 失败后从 ELB 后端摘掉。
- 节点坏了：节点 NotReady 后 Pod 被驱逐并在其它节点重建，驱逐等待时间可以用 `tolerationSeconds` 调短（8.3.9 p365）。装 npd 插件可以检测磁盘、内核、连接跟踪表等节点故障（6.7.7 p263）。
- 可用区故障：手册 21.4.2（p1388）的推荐配置是控制面 3 节点高可用、节点池分布在多个可用区、副本数 ≥ 2、用 podAntiAffinity 把 Pod 分散到不同可用区和节点。

对 new-api：slave 至少 2 个副本、分布在不同节点（最好跨可用区），才谈得上 HA；只跑 1 个副本时，节点故障要等驱逐和重建，中断几分钟。master 所在节点故障时，定时任务暂停到它重建完成，用户请求由 slave 承接。另外配 PodDisruptionBudget（例如 slave `minAvailable: 1`），节点升级、排水时不会把副本一次全赶走。RDS 和 DCS 本身是主备，不受影响。

### ⑫ 日志检索

- CCE 节点默认装 ICAgent，把 Pod 日志上报到 AOM；装云原生日志采集插件后可以接入 LTS，在控制台按工作负载、Pod、时间、关键字统一检索（13.1 p956）。
- new-api 建议去掉 `--log-dir`、日志直接输出到 stdout（标准输出采集的具体配置落地时再确认）。如果继续写文件，ICAgent 只采集 `*.log`、`*.trace`、`*.out`，要按日志策略挂载日志目录。
- `NODE_NAME` 设成 Pod 名，区分是哪个实例写的；每个请求有 request id（`middleware.RequestId`），用户报错时拿 request id 搜就能找到对应日志。
- 使用日志（logs 表）在 RDS 里，后台「使用日志」页面不受影响。

### ⑬ 集群选型

- **版本 1.34 + containerd**：没影响，直接选最高版本。Docker 打的镜像是标准 OCI 格式，containerd 直接能跑，镜像不用重做；Kubernetes 从 1.24 起就只能用 containerd，选低版本也一样。
- **节点 x86**：和现在镜像的架构一致（已确认）。
- **集群类型**：CCE Standard（当前只有这个版本）。网络模型建议选「VPC 网络」，RDS 和 DCS 的安全组放通容器网段（手册 p768）。`TRUSTED_PROXY_CIDR` 要覆盖节点网段和容器网段，否则多一层转发后拿不到用户真实 IP，上线后要看一眼日志里的 IP 对不对。
- **控制面**：3 个控制节点的高可用模式。
- **插件**（手册第 16 章）：
  - 第一阶段：Everest 容器存储、CoreDNS（这两个是必装）、云原生日志采集、Metrics Server、节点故障检测 npd、云原生监控插件；
  - 第二阶段：容器弹性引擎（HPA / 定时扩缩）、集群弹性引擎（自动加减节点）、密钥管理对接 DEW（数据库密码和加密 key 托管）；
  - 暂不需要：NGINX Ingress（已有 ELB）、OpenKruise（以后做灰度再说）、Gatekeeper、Dashboard、Volcano、GPU/NPU 套件。

### ⑭ 运维方式变化

| BMS（Docker） | CCE |
|---|---|
| `docker logs new-api` | `kubectl logs` / 控制台 / LTS |
| `docker exec new-api ...` | `kubectl exec` / 控制台「远程登录」 |
| `docker run --rm ... redis-cli`（查 DCS） | `kubectl run` 起临时 Pod |
| compose `healthcheck` | Deployment 里的 probe |
| `/root/.docker/config.json` 拉镜像 | imagePullSecret，或 SWR 同账号免密 |
| 页面「立即安装」/ watchtower | CCE 控制台「升级」「回退」/ `kubectl set image` |
| 节点上 `docker` 命令 | 节点上只有 `crictl` |

---

## 4. 代码改动清单（等方案确认后再做）

1. 优雅退出：`main.go` 改用 `http.Server` + SIGTERM 处理，刷 batchUpdate，排空审核日志队列。（必须，中等）
2. `StartDirectPaymentExpiryScan`、`StartMediaStorageStatsTask` 加 `IsMasterNode` 判断。（必须，很小）
3. 在线升级：前端隐藏或改造「立即安装」，后端 `PerformUpdate` 下线。（必须，小）
4. CI：`docker-image-ovaijisuan.yml` 增加推送 SWR、使用不可变 tag。（必须，小）
5. 仓库里新增 CCE 部署清单（Deployment master/slave、Service/Ingress、ConfigMap、Secret 模板），以及发布、回退规范（先 master 后 slave、哪些版本不能回退）。（必须，文档和 YAML）
6. 可选：轻量 `/healthz`；Redis 选主，替代 master/slave 拆分。

## 5. 切换思路（概要）

1. 建 CCE Standard 集群（1.34、containerd、x86、VPC 网络模型、3 控制节点），放在同一个 VPC（vpc-newapi）；配 NAT；SWR 推好镜像；装第一阶段插件。
2. 补安全组：RDS 和 DCS 放通容器网段（按集群的网络模型）。
3. 先只部署 master 1 副本，验证启动探测、迁移、日志接入、`/api/status`。这时 BMS 继续对外服务，两边共用同一套 RDS 和 DCS。注意：两边各有一个主节点，定时任务会重复执行，验证时间要短，或者 CCE 这边先设成 slave。
4. WAF 不动，把 ELB 后端从 BMS 切到 CCE（可以先加权分一小部分流量），观察；BMS 容器保留，回滚就是把 ELB 后端切回 BMS。
5. 扩 slave 副本，下线 BMS 上的 new-api 和 watchtower。

## 6. 需要你确认的信息

1. `/opt/new-api/data` 下有哪些文件（`ls -la /opt/new-api/data /opt/new-api/data/wellknown`）。
2. LightX2V NFS 输入盘（媒体存储里的 IngestNFSPath）现网是否启用；SFS 在 BMS 上的挂载路径。
3. 有没有上游厂商或支付渠道配置了 BMS 出口 IP 白名单（迁过去出口 IP 会变）。
4. 第一阶段计划跑几个副本：只跑 1 个的话，第 ② 项可以推后，但就没有 HA。
5. 现有 ELB 是独享型还是共享型（决定能否用就绪联动和慢启动）；HTTPS 证书挂在 WAF 还是 ELB。
6. RDS 的 `max_connections`（决定每个 Pod 的 `SQL_MAX_OPEN_CONNS`）。
7. RDS、DCS 所在可用区，节点池是否跨可用区。

已确认：入口是 WAF + ELB；集群 CCE Standard 1.34 + containerd；节点 x86。
