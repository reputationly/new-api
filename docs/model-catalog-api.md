# 模型目录接口（`GET /api/v1/models/catalog`）

> 面向**直连平台**的第一方客户端。数据源是**唯一一份** Hilo 目录配置（后台「Hilo 目录」页），
> 这里只是把它补上 `platformModel`、`interfaceType` 等客户端要用的字段。
> 与官方 `GET /api/v1/models/config` 的区别：那份**故意不下发**平台模型名（视频报的是
> `MiniMax-H3-FL` 而不是 `minimax-h3-2k`），直连平台的客户端没法拿它提交。

---

## 1. 请求

```http
GET /api/v1/models/catalog
```

- **匿名**（不校验令牌），与 `models/config` 同一套限流。
- 只报**平台上真有渠道**的模型：目录里配了但没有渠道的条目不出现，避免客户端选中后调用才失败。
- 响应带 `Cache-Control: no-cache`，客户端按顶层 `version` 判断要不要重拉。

## 2. 响应

```jsonc
{
  "version": "b874604573e6…",          // 内容哈希（含 defaults），变了就重拉
  "image": [ ModelCatalogItem, … ],
  "video": [ ModelCatalogItem, … ],
  "audio": [ ModelCatalogItem, … ],
  "text":  [ ModelCatalogItem, … ],
  "defaults": {
    "chat": "deepseek-v4-1-flash",     // 默认对话模型（平台模型名）
    "multimodal": "qwen3.8-flash-fp8"  // 多模态识别模型；未配置/不可见/未声明视觉能力时为空
  }
}
```

四个数组**永远存在**（空目录是 `[]`，不是 `null`），保持与 `models/config` 兼容。

### 2.1 ModelCatalogItem

| 字段 | 含义 |
|---|---|
| `id` | 客户端 id（目录条目的 `model.id`） |
| **`platformModel`** | **提交生成时使用的平台模型名**（目录条目的 `platform_model`）；`models/config` 不下发它 |
| `displayName` / `description` | 展示用 |
| **`interfaceType`** | 客户端与服务端约定的**接口/协议类型**，来自目录条目的 `interface_type`（如 `maas-image` / `maas-video` / `chat-completion`）。**`text` 条目留空时缺省为 `chat-completion`；媒体条目留空则不下发该字段**，由客户端自行决定。写错或不认识时客户端应回退到自己的默认映射 |
| `maxRefs` | 参考素材上限（媒体条目） |
| `params` | 参数表（与 `models/config` 同结构，可直接渲染表单） |
| `supportsImage` / `supportsVideo` / `supportsAudio` | 仅 `text` 条目：该对话模型能接收的**输入**模态；`text` 恒支持、不列出 |
| `defaults.chat` / `defaults.multimodal` | 角色默认值，来自目录的 `default_text_model` / `multimodal_model`，只报"真的下发出去"的模型 |

## 3. 与其它接口的关系

| 接口 | 数据来源 | 用途 | 含平台模型名 |
|---|---|---|---|
| `GET /api/v1/models/catalog`（本文） | **Hilo 目录**（过滤掉没渠道的） | 直连平台的第一方客户端 | ✅ |
| `GET /api/v1/models/config` | **Hilo 目录**（过滤掉没渠道的） | 官方 gateway（蒜狸小助手） | ❌（故意） |
| `GET /v1/ov/capabilities` | **这个令牌实际能调什么**（abilities/分组/令牌白名单/聚合模型），目录只用来补参数表、玩法、`inputModalities`、`defaults` | 按令牌裁剪的能力视图 | ✅ |

两点边界要说清，否则容易把"目录"和"能力"混成一件事：

- **配置只有一份：Hilo 目录。** 上面两个 `models/*` 接口都完全由它派生；不要在任何地方再引入第二份
  配置（历史上"通用设置里再填一遍默认模型/模态"就是因为双份来源被删掉的）。
- **`/v1/ov/capabilities` 不是目录的第二份拷贝**：它的主体是"这把 key 真能调到哪些模型"，
  目录只给它补描述性字段。所以两者的条目集合可以不一样 ——
  **对话模型**由 abilities 驱动（没列进目录 `text` 段、但有渠道的模型照样出现在 capabilities，
  只是没有 `inputModalities`）；**媒体模型**只报目录里的条目。目录里配了但没渠道的，两边都不会出现。


## 4. 变更 `interface_type` 时注意

- 该字段**不做枚举校验**（客户端协议名可能是自定义的，如 `minimax-text-native`），
  写错不会报错，只会原样下发 —— 客户端遇到不认识的值必须回退到自己的默认映射。
- 它和 `transport` 不是一回事：`transport` 决定 **new-api 转发上游**用哪套协议
  （`images` / `videos_task` / `chat` / `audio_speech`），`interface_type` 决定
  **客户端调用 new-api** 用哪套协议。改一个不要以为另一个会跟着变。
