# CCE 高可用架构规划

域名：`maas.ovaijisuan.com`

---

## 目录

1. [CCE 是什么](#cce-是什么)
2. [架构演进路线](#架构演进路线)
3. [CCE 集群配置参数](#cce-集群配置参数)
4. [网络地址规划](#网络地址规划)
5. [阶段二：纳管 BMS 节点](#阶段二纳管-bms-节点)
6. [阶段三：new-api 迁移为 K8s Deployment](#阶段三new-api-迁移为-k8s-deployment)
7. [升级机制改造](#升级机制改造)
8. [Streaming / 长连接配置](#streaming--长连接配置)
9. [多副本兼容性](#多副本兼容性)
10. [推荐插件](#推荐插件)
11. [验证清单](#验证清单)

---

## CCE 是什么

CCE（Cloud Container Engine，云容器引擎）是华为云的托管 Kubernetes 服务，等同于 AWS EKS / Google GKE。

### 与多 BMS 方案的关系

CCE 本质上是"多 BMS 实例 + Kubernetes 调度层"的组合。之前分析多 BMS 时存在的问题，在 CCE 中同样存在，解决方式也相同：

| 问题 | 多 BMS | CCE | 解决方案 |
|---|---|---|---|
| 在线升级中断 | 5-10 秒停机 | 单副本同样有中断；多副本可近似零停机 | 多副本 + 滚动更新 |
| Streaming 被切断 | 升级时切断 | Pod 终止时切断 | `terminationGracePeriodSeconds: 330` |
| 长连接超时 | WAF/ELB 配置 | 同上，WAF/ELB 配置仍适用 | WAF 读超时 300s |
| 额度双花 | 进程内存不同步 | Pod 间内存不同步 | `BATCH_UPDATE_ENABLED=false` |

### CCE 相比多 BMS 的增量价值

- **滚动升级**：多副本时未被更新的 Pod 继续服务，整体不停机
- **自动重启**：Pod 崩溃后 kubelet 自动重建，无需人工介入
- **健康检查**：readiness probe 确保新版本就绪后才接流量
- **HPA 弹性伸缩**：按 CPU/内存自动扩缩 Pod 数量
- **统一调度**：节点故障时 Pod 自动迁移到其他节点

### 集群结构

```
CCE 集群（newapi）
├── 控制面（Master × 3）── 华为云完全托管，用户不操作
│   ├── kube-apiserver
│   ├── kube-controller-manager
│   ├── kube-scheduler
│   └── etcd
│
└── 工作节点（Worker Node）── 实际运行业务的服务器
    ├── BMS（bms-newapi，纳管后加入）── new-api Pod × N
    └── ECS（按需添加）
```

- **3 实例（Master）**：控制面高可用，3 个 Master 分布在不同可用区，Master 故障不影响业务 Pod 运行
- **50 节点（集群规模）**：Worker Node 的最大容量上限，不是当前节点数量。创建集群时为 0 个 Worker，需手动添加

---

## 架构演进路线

```
阶段一（当前，已完成）
  BMS + docker-compose + nginx（443）
  → 引入 ELB + WAF，停 nginx，WAF 直接回源 BMS:3000

阶段二（下一步）
  CCE 集群创建 → 纳管 BMS 为 Worker Node
  new-api 仍以 docker-compose 运行（暂不迁移，验证集群稳定性）

阶段三（稳定后）
  new-api 迁移为 K8s Deployment，停 docker-compose
  WAF 回源地址：BMS 私有 IP:3000 → K8s Service ClusterIP:3000
  升级方式：kubectl rollout restart（或 CI/CD 自动触发）

阶段四（可选，按需扩容）
  添加 ECS Worker Node，实现真正多节点
  new-api replicas 增加到 2+，实现零停机滚动升级
  开启 HPA，应对突发流量
```

---

## CCE 集群配置参数

创建集群时的建议参数（部分创建后不可修改，务必确认）：

| 参数 | 建议值 | 说明 |
|---|---|---|
| 集群版本 | v1.31 | 推荐最新稳定版 |
| 控制节点架构 | **X86** | BMS 是 Intel X86，保持架构一致 |
| 集群规模 | 50 节点 | 当前够用，可往上升级规格，不能降级 |
| Master 实例数 | 3 实例（高可用） | 生产环境必须选 3 实例 |
| 虚拟私有云 | vpc-newapi (10.0.0.0/24) | 创建后不可修改 |
| 默认节点子网 | 10.0.0.0/24 | 当前唯一子网 |
| 容器网段（Pod CIDR） | **172.16.128.0/18** | 见下方网络规划，不能与现有段冲突 |
| 服务网段（Service CIDR） | **10.247.0.0/16** | 标准推荐值 |
| 服务转发模式 | iptables | 默认值，适合当前规模 |
| 开启过载控制 | 开启 | 默认即可 |

> **容器网段和服务网段创建后不可修改**，务必在创建前确认正确。

---

## 网络地址规划

### 地址段冲突分析

| 网段 | 用途 | 冲突风险 |
|---|---|---|
| 10.0.0.0/24 | 当前子网（BMS、RDS、NAT 等） | 容器网段不能包含此段 |
| 172.16.8.0/23 | 对等连接路由（peering-f0e8） | 容器网段不能与此重叠 |
| 173.2.0.0/16 | 对等连接路由（peering-f0e8） | 容器网段不能与此重叠 |
| 100.64.0.0/10 | 链路本地（系统路由） | 容器网段不能与此重叠 |

### 建议配置

```
VPC 网段：        10.0.0.0/16（当前 VPC 总范围，含子网 10.0.0.0/24）
节点子网：        10.0.0.0/24（现有子网，节点 IP 从此分配）
容器网段：        172.16.128.0/18（避开 172.16.8.0/23，两者不重叠）
服务网段：        10.247.0.0/16（不与任何现有段冲突）
```

> **子网容量提示**：10.0.0.0/24 仅 256 个 IP，已用约 23 个。若后续添加大量 Worker Node（每个节点消耗 1 个子网 IP），建议提前在 vpc-newapi 下新增一个 /22 子网（1022 个 IP）专给 CCE Worker Node 使用。

---

## 阶段二：纳管 BMS 节点

### 什么是纳管

纳管（接管现有节点）是将已有服务器加入 CCE 集群的方式。纳管的节点在集群删除时**不会被删除**，风险可控。

纳管后：
- CCE 自动在 BMS 上安装 kubelet、kube-proxy、containerd 等 K8s 组件
- BMS 原有的 Docker 和 docker-compose 服务**不受影响**，可继续运行
- 可以逐步将服务从 docker-compose 迁移到 K8s，无需停机切换

### 纳管前提条件

1. BMS 操作系统：EulerOS 2.9/2.10 或 Huawei Cloud EulerOS 2.0（确认 BMS 系统版本）
2. BMS 安全组已放行 CCE 所需端口（见 CCE 文档 5.4.4）
3. BMS 私有 IP 在 10.0.0.0/24 子网内

### 纳管操作步骤

```
CCE 控制台 → 集群 newapi → 节点管理 → 纳管节点
→ 填写 BMS 私有 IP
→ 等待安装 K8s 组件（约 5-10 分钟）
→ 节点状态变为"运行中"
```

### 纳管后验证

```bash
# 在 BMS 上确认 kubelet 运行
systemctl status kubelet

# 在本地（配置 kubeconfig 后）确认节点就绪
kubectl get nodes
# 期望：bms-newapi 状态为 Ready

# 确认 docker-compose 服务仍正常
docker ps
curl http://127.0.0.1:3000/api/status
```

---

## 阶段三：new-api 迁移为 K8s Deployment

### Deployment YAML

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: new-api
  namespace: default
spec:
  replicas: 1                            # 单 BMS 阶段先用 1 副本
  selector:
    matchLabels:
      app: new-api
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0                  # 滚动期间不减少可用 Pod
      maxSurge: 1                        # 先启动新 Pod，再停旧 Pod
  template:
    metadata:
      labels:
        app: new-api
    spec:
      terminationGracePeriodSeconds: 330 # 等待 streaming 请求完成（> WAF 读超时 300s）
      nodeSelector:
        kubernetes.io/hostname: bms-newapi  # 固定调度到 BMS 节点（单节点阶段）
      containers:
      - name: new-api
        image: <registry>/new-api:latest
        ports:
        - containerPort: 3000
        envFrom:
        - secretRef:
            name: new-api-env            # 从 K8s Secret 读取 .env.prod 配置
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sh", "-c", "sleep 10"]  # 给 ELB 摘流缓冲时间
        readinessProbe:                  # 就绪后才接流量
          httpGet:
            path: /api/status
            port: 3000
          initialDelaySeconds: 5
          periodSeconds: 3
          failureThreshold: 3
        livenessProbe:                   # 存活检测，失败自动重启
          httpGet:
            path: /api/status
            port: 3000
          initialDelaySeconds: 15
          periodSeconds: 10
          failureThreshold: 3
        resources:
          requests:
            cpu: "2"
            memory: "4Gi"
          limits:
            cpu: "8"
            memory: "16Gi"
```

### Service YAML（WAF 回源目标）

```yaml
apiVersion: v1
kind: Service
metadata:
  name: new-api
  namespace: default
  annotations:
    kubernetes.io/elb.keepalive_timeout: '3600'  # ELB 空闲超时，支持长连接
spec:
  selector:
    app: new-api
  ports:
  - port: 3000
    targetPort: 3000
    protocol: TCP
  type: ClusterIP
```

迁移到 K8s 后，WAF 回源地址从 `BMS私有IP:3000` 改为 `Service ClusterIP:3000`（或保持 BMS IP，因为 Service 也在同一节点上）。

### 环境变量迁移

```bash
# 将 .env.prod 转为 K8s Secret
kubectl create secret generic new-api-env \
  --from-env-file=.env.prod \
  -n default

# 验证
kubectl get secret new-api-env -o yaml
```

---

## 升级机制改造

### 当前机制（BMS docker-compose）

```
管理员点"立即安装"
→ POST /api/system/update
→ controller/system.go:PerformUpdate() 调用 Watchtower API
→ Watchtower 通过 Docker API 拉新镜像并重启容器
→ 前端轮询 /api/status 检测版本变化
```

### 迁移到 CCE 后的问题

Watchtower 依赖 Docker daemon（`/var/run/docker.sock`），无法感知或操作 K8s Pod。
迁移后**升级按钮（立即安装）将失效**，但"检查更新"（查 GitHub 最新版本）仍正常。

### 改造方案

**方案 A：kubectl rollout（推荐，改动最小）**

修改 `controller/system.go:PerformUpdate()`，将 Watchtower 调用替换为：

```go
// 触发 K8s 滚动重启（镜像 tag 不变时用此方式）
cmd := exec.Command("kubectl", "rollout", "restart",
    "deployment/new-api", "-n", "default")
```

前端轮询 `/api/status` 的逻辑**完全不需要改**，新 Pod 就绪后版本号变化，前端自动刷新。

**方案 B：kubectl set image（版本号精准控制）**

```go
// 指定具体镜像 tag，触发拉取新镜像的滚动更新
cmd := exec.Command("kubectl", "set", "image",
    "deployment/new-api",
    fmt.Sprintf("new-api=%s:%s", imageRepo, newVersion),
    "-n", "default")
```

**方案 C：CI/CD 自动化（最优，无需改业务代码）**

GitHub Actions 工作流：代码合并 main → 自动 build → push 镜像 → `kubectl set image` → K8s 自动滚动更新。前端升级按钮改为触发 workflow dispatch。

---

## Streaming / 长连接配置

### 问题来源

K8s 默认 `terminationGracePeriodSeconds` 为 **30 秒**。AI streaming 请求可能持续 2-5 分钟，Pod 终止时 30 秒后被强制 kill，正在进行的 streaming 响应被中断。

### 完整解决链路

```
ELB 层：
  空闲超时 3600s（已在 ELB 监听器配置）

WAF 层：
  读超时 300s（已在 WAF 超时配置开启）

K8s Pod 层（本文档新增）：
  terminationGracePeriodSeconds: 330s    > WAF 读超时 300s，确保请求能完成
  preStop: sleep 10s                     给 ELB 摘流 10 秒，防止新请求进入旧 Pod
  
Gin 层（new-api 内部）：
  Gin 收到 SIGTERM 后进入 graceful shutdown
  等待当前请求处理完成后退出
```

### 滚动升级时的流量切换时序

```
触发 kubectl rollout restart
    ↓
新 Pod 启动 → readinessProbe 通过 → 开始接收新请求
    ↓
旧 Pod 收到 SIGTERM → 执行 preStop（sleep 10s）
    ↓
10s 内：ELB 将旧 Pod 从后端摘除，新请求全部路由到新 Pod
    ↓
preStop 完成 → Gin graceful shutdown 开始
    ↓
最多等待 330s：旧 Pod 内正在进行的 streaming 请求继续处理
    ↓
所有请求完成（或达到 330s 上限）→ 旧 Pod 销毁
```

---

## 多副本兼容性

扩容到 2+ 副本时必须处理以下问题：

### 必须处理：关闭 BATCH_UPDATE

`BATCH_UPDATE_ENABLED=true` 时，每个 Pod 将 quota 扣减累积在**进程内存**中，多 Pod 内存互相不可见，导致额度双花。

```env
BATCH_UPDATE_ENABLED=false
```

性能影响：每次请求实时写 DB。RDS 规格（32 核 64GB）完全可以承受。

### 兼容，无需处理

| 项目 | 结论 | 说明 |
|---|---|---|
| 管理台配置 | 完全兼容 | 所有配置存 PostgreSQL，其他节点 60s 内同步 |
| Session / 登录态 | 完全兼容 | JWT 无状态，`SESSION_SECRET` 固定即可 |
| Redis 缓存 | 完全兼容 | 所有 Pod 共享同一 Redis 实例 |
| 长连接 | 可接受 | streaming 请求绑定当前 Pod，Pod 故障需客户端重试 |

---

## 推荐插件

按优先级排序：

| 优先级 | 插件 | 用途 |
|---|---|---|
| 必装 | CoreDNS | 集群 DNS，系统必备 |
| 必装 | Everest（CCE 容器存储） | 挂载云硬盘、持久化日志 |
| 推荐 | CCE 节点故障检测 | 自动检测并驱逐故障节点 |
| 推荐 | autoscaler + cce-hpa-controller | HPA 工作负载弹性伸缩 + 节点弹性伸缩 |
| 推荐 | 云原生监控插件（Prometheus + Grafana） | 监控 Pod CPU/内存/请求量 |
| 推荐 | 云原生日志采集插件 | 容器日志采集到 LTS，集中查询 |
| 暂缓 | CCE AI 套件（NVIDIA GPU / Ascend NPU） | 如果 Worker Node 有 GPU/NPU 再装 |
| 暂缓 | Volcano 调度器 | AI 批量任务调度，当前不需要 |

---

## 验证清单

### 纳管 BMS 后验证

```bash
# 1. 节点状态正常
kubectl get nodes
# 期望：bms-newapi   Ready   <age>

# 2. docker-compose 服务不受影响
docker ps | grep new-api
curl http://127.0.0.1:3000/api/status

# 3. 系统资源未被 K8s 组件大量占用
top -b -n1 | head -20
```

### new-api 迁移后验证

```bash
# 1. Pod 运行正常
kubectl get pods -n default
# 期望：new-api-xxx   1/1   Running

# 2. 通过域名完整链路访问
curl -v https://maas.ovaijisuan.com/api/status
# 期望：200，body 包含 version 字段

# 3. Streaming 不截断（手动测试）
# 发送预期持续 60 秒以上的 AI streaming 请求，观察响应是否完整

# 4. 滚动升级测试
kubectl rollout restart deployment/new-api
kubectl rollout status deployment/new-api
# 期望：升级过程中域名访问无中断（单副本有短暂中断，多副本无中断）

# 5. 升级后版本变化
curl https://maas.ovaijisuan.com/api/status | grep version
```
