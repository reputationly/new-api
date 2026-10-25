# 图片/视频水印功能设计（已确认版）

> 基于 origin/main `e76c82ca6`。配置参照内容审核（`setting/system_setting/moderation.go`、
> `controller/moderation_policy.go`、`SettingsModerationPolicy.jsx`）。

## 0. 已确认的决策

| # | 问题 | 结论 |
|---|---|---|
| 1 | 客户端要 `response_format=b64_json` | 对绑定了水印的分组，在 b64 上加水印后返回（不落 OBS） |
| 2 | 渠道「成品 URL 直接透传」/ 媒体存储或上游 URL 落盘关闭 | **不碰**。只有本来就要落我方 OBS 的产物才加水印 |
| 3 | Vertex data: 视频、Midjourney、实时拉取 | 第一期不覆盖（现网没有这些渠道） |
| 4 | `task:` 引用上一产物作输入 | 统一读**原片**，不叠加水印 |
| 5 | 加水印失败 | 交付无水印原片；记日志 + 落失败记录表，运营设置页显示告警 |
| 6 | 水印类型 | 只做文字，不做 logo。初始模板文案「AI生成」 |

## 1. 范围

- 我方产物（图片、视频）在上传 OBS 之前压文字水印；OBS 上 `<key>` 存带水印版，用户拿到的签名 URL 都是带水印的。
- 原片另存 `<key 去扩展名>.orig.<ext>`（同目录，前缀类生命周期规则同样覆盖），**只**用于 `task:` 引用作下游输入，不对用户暴露。
- 不处理：用户输入卸载（`relay/task_media_offload.go`）、审核取证桶、用户素材桶、透传 URL、b64 以外的不落 OBS 路径。

## 2. 挂点（全部在 `service/media_ingest.go`）

| 链路 | 入口 | 分组 | 来源标记 |
|---|---|---|---|
| 异步任务成品（含聚合流水线生成段成品、最终成片） | `PersistTaskResultToOBS` → `persistToOBS` | `task.Group` | `task` |
| 自建同步生图 | `PersistImageNFSToOBS`（新增 group 参数，调用方传 `info.UsingGroup`） | 同左 | `sync_image` |
| 第三方同步生图 url / b64 落盘 | `RewriteImageResponseToOBS`（新增 group 参数）→ `persistThirdPartyImage` | 同左 | `third_party_image` |
| 第三方同步生图 b64_json 直返 | `RewriteImageResponseToOBS` 的 `wantB64` 分支 | 同左 | `b64_image` |

聚合流水线：超分段输入是生成段 NFS 原片（`submitUpscaleTask(..., nfsPath)`），不受影响；生成段成品与最终成片各自落盘时各压一次。

## 3. 配置

`config.GlobalConfig.Register("watermark", ...)`，options 键 `watermark.enabled / templates / default_binding / group_bindings`。

```go
type WatermarkTemplate struct {
    Name      string  `json:"name"`       // 唯一、非空、≤32 字符、不能为 "none"
    Text      string  `json:"text"`       // 非空、≤50 字符、无控制字符
    Position  string  `json:"position"`   // top_left|top_right|bottom_left|bottom_right|center
    FontScale float64 `json:"font_scale"` // 字号 = 短边 × font_scale，0.01~0.2
    Opacity   float64 `json:"opacity"`    // 0.05~1
    Color     string  `json:"color"`      // #RRGGBB
}
type WatermarkBinding struct {
    Image string `json:"image"` // ""=继承默认（仅分组）；"none"=不加；否则模板名
    Video string `json:"video"`
}
type WatermarkSettings struct {
    Enabled        bool                        `json:"enabled"`
    Templates      []WatermarkTemplate         `json:"templates"`
    DefaultBinding WatermarkBinding            `json:"default_binding"`
    GroupBindings  map[string]WatermarkBinding `json:"group_bindings"`
}
```

默认值：`Enabled=false`；一个模板 `{默认, AI生成, bottom_right, 0.04, 0.6, #FFFFFF}`；`DefaultBinding={默认, 默认}`；无分组绑定。
总开关关闭时任何链路行为与现在完全一致。

解析 `ResolveTemplate(group, kind)`：关 → 无；分组绑定该字段非空用它，否则用默认绑定；`"none"`/空 → 无；模板名查不到 → 无。

保存：`PUT /api/watermark/config`（RootAuth）一次提交四项，校验引用完整性（同 `SaveModerationPolicyConfig` 的理由）。通用 `PUT /api/option` 对 `watermark.*` 也做同样校验，防绕过。

## 4. 渲染（`service/watermark`）

- 图片、视频统一 ffmpeg `drawtext`，`exec.CommandContext` 直接传参，不经 shell。
- 文案通过 `textfile=` 传入（写临时文件），不把用户文本拼进 filter 字符串。
- 字体：Dockerfile 安装 `fonts-wqy-microhei`，默认 `/usr/share/fonts/truetype/wqy/wqy-microhei.ttc`，环境变量 `WATERMARK_FONT_FILE` 可覆盖。
- 字号/边距用 ffprobe 取宽高，在 Go 里按短边计算（短边对 90° 旋转不变）；边距 = 短边 × 3%；黑色描边保证白底可读。
- 输出格式与扩展名保持不变：png（保留 alpha）/ jpg / webp（libwebp）；视频 mp4/mov 用 `libx264 -preset veryfast -crf 20 -pix_fmt yuv420p`，音轨 `-c:a copy`（无音轨也可），`+faststart`。其它格式（gif、webm 等）视为「不支持」→ 按失败处理（交付原片 + 记录）。音频等非图非视频产物不处理、不算失败。
- 类型判断：按 key 扩展名；无扩展名时按任务 action（`constant.IsImageTaskAction`/`IsVideoTaskAction`）兜底，并用 png/mp4 封装。
- 超时：图片 30s，视频 300s。进程内并发：图片 8、视频 2（按 ctx 等待）。

本机实测（8 核，合成素材）：图片 0.05–0.09s；10s 720p 视频约 1s；10s 1080p 约 1.7–2.3s，峰值内存 330–380MB。

## 5. 落盘流程（`persistToOBS` 等统一走一个函数）

1. 未命中模板 → 原样 `mediastore.Persist`（与现在一致）。
2. 把来源落到本地文件：NFS 校验后直接用；`Data` 写临时文件；上游 URL 用 mediastore 现有的带 SSRF/host/大小校验的下载逻辑（新导出）。下载失败 = 落盘失败（沿用现有失败语义）。
3. ffmpeg/字体不可用或渲染失败 → 记录失败，把原片落到 `<key>`（交付原片）。
4. 成功 → 先落原片 `<orig key>`，再落水印版 `<key>`（metadata `watermark=1`）。任一步上传失败 = 落盘失败，沿用现有重试/降级语义。

b64_json 直返：渲染成功替换 `b64_json`；失败保留原 b64 并记录失败。

## 6. task: 引用读原片

- `nfsinput.ResolveTaskRefBytes`：obs 引用先读 NFS（自建产物在 NFS 上本来就是原片，不变）；NFS 读不到时优先下载 `<orig key>`（存在才用），否则退回 `<key>`。
- `taskRefResolver.signFromOBS`（第三方直签）：优先签 `<orig key>`（Head 确认存在），否则签 `<key>`。
- 开启水印之前产生的历史产物没有原片副本，行为不变。

## 7. 失败告警

- 新表 `watermark_failures`（GORM AutoMigrate，三库兼容）：时间、来源、媒体类型、分组、用户 ID、任务 ID、对象 key、错误（截断 1000 字符）。主节点每天清理 30 天前记录。
- `GET /api/watermark/status`（AdminAuth）：ffmpeg/字体是否就绪、近 24h/7d 失败数、最近 20 条。
- 运营设置页水印卡片顶部：环境未就绪 → 红色告警；近 24h 有失败 → 黄色告警 + 最近失败列表。

## 8. 前端

运营设置 → 审核策略卡片下方新增「水印设置」卡片：状态告警、总开关、模板表（增删改 + 预览）、默认绑定、分组绑定表（分组 | 图片 | 视频，选项：继承默认 / 不加 / 模板名；显示已删除分组的孤儿绑定）。预览：`POST /api/watermark/preview` 用内置样图渲染返回。

## 9. 存储成本

开启水印的产物在 OBS 上存两份（水印版 + 原片），这部分产物的 OBS 用量约翻倍。
