# 视频模型接入手册（客户端视角）

> 面向直接调 API 的集成方。覆盖：发现能力 → 提交 → 轮询 → 取消 → 错误处理。
> 服务端实现见本地 `docs/video-multimodal-plan.md`（该文件不入库）；本文只写客户端要做什么。

---

## 1. 发现能力

```http
GET /v1/ov/capabilities
Authorization: Bearer <sk-...>
```

返回里每条 `capability` 至少包含：

| 字段 | 含义 |
|---|---|
| `id` / `model` | 提交时 `model` 字段用哪个名字（聚合模型报的也是它自己） |
| `operation` | `generate_image` / `generate_video` / `generate_speech` / `generate_music` / `chat` |
| `transport.kind` | 走哪条协议：`images` / `videos_task` / `chat` / `audio_speech` |
| `limits` | 顶层上限：参考图数、时长、输入字节 |
| `taskTypes` | 模型声明的 task_type 集合（仅 videos_task） |
| **`modes`** | **玩法列表**（见下） |
| **`deliveryDurations`** | 实际可交付时长阶梯（仅带帧栅格的引擎，如 H3） |
| `paramSchema` | 参数表（`type/properties/enum/default/title`），可直接渲染成表单 |
| `default` | 该 `operation` 下的默认模型。**同一操作恰好一条为 `true`**（管理员配的默认模型不可见时回退按顺序第一条） |
| `inputModalities` | 仅 `chat` 条目可能非空：该对话模型能接收的**输入**模态 `image` / `video` / `audio`；`text` 恒支持、不列出。空表示未声明，客户端按兼容处理。注意与 OpenAI 请求里的 `modalities`（输出模态）不是一回事 |

### 1.1 modes（玩法）

`modes` 为空 = 单一玩法，按顶层 `limits` 提交即可。非空时每个 mode 形如：

```jsonc
{
  "id": "first-last-frame",
  "label": { "zh": "首尾帧", "en": "First and last frame" },
  "taskType": "flf2v",
  "taskTypeIfSingleImage": "i2v",      // 只给 1 张图时改发这个
  "references": {
    "image": { "min": 1, "max": 2 },   // 这个玩法能吃多少
    "video": { "min": 0, "max": 0 },
    "audio": { "min": 0, "max": 0 }
  },
  "requiresSourceVideo": false,
  "note": { "zh": "…", "en": "…" }
}
```

H3 的四类玩法：

| mode | task_type | 图 | 视频 | 音频 | 备注 |
|---|---|---|---|---|---|
| `text-to-video` | `t2v` | 0 | 0 | 0 | 纯提示词 |
| `first-last-frame` | `flf2v`（1 张图 → `i2v`） | 1–2 | 0 | 0 | 两张=首+尾，一张=首帧 |
| `reference` | `r2va` | ≤9 | ≤3 | ≤3 | 视频+音频合计 ≤3；至少给一项 |
| `video-extension` | `continuation` | 0 | 1 | 0 | 必须给 `metadata.source_video`；**只** 768P |

> 别拿顶层 `limits.maxReferenceImages` 当玩法的上限：那个数是"这个模型最多能收几张"，
> 不是"这个玩法收几张"。首尾帧塞 9 张图会被网关直接 400。

### 1.2 deliveryDurations（交付时长阶梯）

带帧栅格的引擎（H3：24fps，帧数向上吸附到 `17n+5`）**请求时长 ≠ 交付时长**：

```jsonc
"deliveryDurations": [1.625, 2.333, 3.042, 4.458, 5.167, 5.875, 6.583, 7.292, 8.0, …]
```

`deliveryDurations[i]` 对应"请求 i+1 秒"。用它做进度条/时长展示；最终以响应里的
`metadata.duration_seconds` 为准（见 §3）。

---

## 2. 提交

```http
POST /v1/videos
Authorization: Bearer <sk-...>
Content-Type: application/json
```

```jsonc
{
  "model": "minimax-h3-2k",
  "prompt": "…",                       // 必需（H3 上限 7000 字符）
  "duration": 10,                      // 秒，整数；续写时=最终总时长
  "image":  "https://…/first.png",     // 首帧；或 "images": [首帧, 尾帧]
  "metadata": {
    "image_mode": "first-last-frame",  // 玩法，见 §1.1
    "resolution": "768P",              // 768P | 2K（2K 会走超分，耗时更长）
    "generate_audio": true,
    "source_video": "https://…/a.mp4"  // 仅 video-extension
  }
}
```

要点：

- `image_mode` 与 `task_type` 等价，二选一；同时给时以 `task_type` 为准。
- 参考族的素材放 `metadata.src_ref_images` / `reference_videos` / `reference_audios`
  （与 doubao 同名），**不要**放顶层 `images`（那会被当成首帧）。
- **续写**：`duration` 是最终总时长，不是"延长多少秒"。源片 2–15 s、短边 ≥256px、
  ≤50 MB；总时长必须大于源片（最少 +1 s）。交付固定 768P。
- 2K 是"生成 768P + 超分"，耗时约为 768P 的数倍；20 s 请先用 768P。

响应（异步）：

```json
{ "id": "task_…", "object": "video", "status": "queued", "progress": 0, "created_at": 1795… }
```

---

## 3. 轮询

```http
GET /v1/videos/{id}
```

```jsonc
{
  "id": "task_…", "status": "completed", "progress": 100,
  "model": "minimax-h3-2k",
  "seconds": "10",                       // 你请求的整数秒
  "metadata": {
    "url": "https://…/signed.mp4",       // 产物（签名 URL，有时效）
    "duration_seconds": 10.125,          // 真实交付时长
    "frames": 243,
    "requested_seconds": 10,
    "continuation": {                    // 仅续写
      "source_seconds": 3,
      "generate_seconds": 7,
      "total_seconds": 10,
      "concat_done": true,
      "concat_error": ""                 // 拼接失败时非空（此时交付的是生成段）
    }
  }
}
```

- 状态：`queued` / `in_progress` / `completed` / `failed` / `cancelled`。
- 建议轮询间隔 3–5 s；1 s 档约 45 s，20 s 档约 7–8 分钟（单实例串行，排队另计）。
- 失败时带 `error.message`（上游原因会透出来）。

## 4. 取消

```http
DELETE /v1/videos/{id}
```

排队中取消 → 全额退；进行中取消 → 尽力中止、按规则退。终态取消幂等。

---

## 5. 错误码

| code | 触发 | 客户端该做什么 |
|---|---|---|
| `invalid_request` | 参数缺失/类型错 | 读 `message` 修参数 |
| `duration_out_of_window` | 超出模型时长窗口 | 按 `deliveryDurations` 或目录窗口折算 |
| `duration_below_source` | 续写总时长 ≤ 源片 | 把 `duration` 调大 |
| `duration_not_deliverable` | 生成段太短，引擎交不出 | 同上 |
| `source_too_short` / `source_too_long` / `source_too_small` | 续写源片不合规 | 换源片 |
| `source_unreadable` / `source_missing` | 源片取不到/没给 | 检查 URL 可访问、或改 `task:<id>` 引用 |
| `reference_limit_exceeded` | 参考数量超玩法上限 | 按 §1.1 的 `references` 收 |
| `local_media_failed` / `frame_extract_failed` | 本地抽帧/拼接失败 | 重试；持续失败请联系运维 |
| `upstream_error` / `upstream_timeout` | 引擎侧 | 稍后重试 |

---

## 6. 计费口径（对账用）

- 按秒计价：乘的是**交付秒数**（1 s 请求按 1.625 s 计），见 `metadata.duration_seconds`。
- 续写：只对**生成段**计时长（源片不重复计费）；`metadata.continuation.generate_seconds` 就是计费秒数。
- 2K：生成段 + 超分段各按自己的档位记一条日志，`pipeline_stage` 区分。

---

## 7. 三个常见坑

1. **把 `duration` 当"延长多少"**（续写）—— 它是最终总时长。
2. **首尾帧只给一张图却发 `task_type=flf2v`** —— 网关会 400；按
   `taskTypeIfSingleImage` 改发 `i2v`。
3. **用顶层 `limits.maxReferenceImages` 填首尾帧** —— 应按 `modes[].references` 填。
