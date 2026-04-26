# ComfyUI 渠道集成方案设计

**文档版本：** v1.0  
**日期：** 2026-04-21  
**适用项目：** new-api（github.com/QuantumNous/new-api）

---

## 目录

1. [背景与目标](#一背景与目标)
2. [整体架构](#二整体架构)
3. [ComfyUI 端配置要求](#三comfyui-端配置要求)
4. [new-api 修改清单](#四new-api-修改清单)
5. [API 使用方式](#五api-使用方式)
6. [计费方案](#六计费方案)
7. [多实例调度](#七多实例调度)
8. [Workflow 配置指南](#八workflow-配置指南)
9. [实施顺序](#九实施顺序)

---

## 一、背景与目标

### 1.1 背景

new-api 是一个统一的 AI API 网关，聚合了 40+ 个 AI 服务提供商，提供鉴权、计费、限流和多租户隔离能力。ComfyUI 是业界广泛使用的开源图像生成工作流引擎，通过节点图（Node Graph）组合模型与后处理流程，支持 SDXL、Flux、SD3 等主流模型。

企业部署多个 ComfyUI GPU 节点时面临以下问题：

- 各实例直接暴露，缺乏统一鉴权与配额管理
- 实例间无自动负载均衡，GPU 利用率不均衡
- 工作流通过 IP/端口区分，缺乏语义化命名（用户无法选择"肖像风格"或"风景风格"）
- 无统一计费记录，无法对不同用户组做差异化定价

### 1.2 目标

将 ComfyUI 作为新渠道类型（`ChannelTypeComfyUI = 58`）集成进 new-api，实现：

| 目标 | 实现方式 |
|------|---------|
| **多实例负载均衡** | 每个 ComfyUI 实例对应一条渠道记录，复用现有权重调度系统，零新增代码 |
| **工作流语义化命名** | `"model": "comfyui-portrait"` 映射到对应工作流 JSON 模板 |
| **统一计费** | 按次扣除配额，复用异步任务（TaskAdaptor）计费框架 |
| **多租户隔离** | 复用现有 token/group 权限体系 |

### 1.3 选型说明：为何不用中间层（comfyui-deploy 等）

| 方案 | 问题 |
|------|------|
| comfyui-deploy 中间层 | 无计费、与 new-api 调度逻辑重叠、维护两套系统 |
| 原生 TaskAdaptor（本方案） | 直连 ComfyUI REST API，new-api 统一处理鉴权/计费/调度 |

---

## 二、整体架构

### 2.1 数据流

```
用户客户端
    │
    │  POST /v1/images/comfyui/generations
    │  {"model": "comfyui-portrait", "prompt": "beautiful sunset..."}
    │
    ▼
new-api (TokenAuth → Distribute)
    │
    │  1. Ability 表按 model 名匹配渠道
    │  2. 按权重选取一个 ComfyUI 渠道实例
    │  3. RelayTask Controller
    │
    ▼
ComfyUI TaskAdaptor
    │
    │  ValidateRequestAndSetAction
    │    → 从 ChannelOtherSettings.ComfyUIWorkflows["comfyui-portrait"]
    │      读取 workflow 模板配置
    │
    │  BuildRequestBody
    │    → 将用户 prompt 注入 workflow JSON 的 PromptNodeID 节点
    │    → 随机生成 seed 注入 SeedNodeID 节点
    │
    │  POST http://comfyui-host:8188/prompt
    │    → 返回 {"prompt_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"}
    │
    │  DoResponse
    │    → 存储 upstream_task_id = prompt_id
    │    → 向客户端返回 {"id": "task_xxx", "status": "submitted"}
    │
    ▼
DB: tasks 表
(platform="58", upstream_task_id=prompt_id, status=SUBMITTED)

    ┌──────────────────────────────────────────────────────────┐
    │  后台轮询（每 15 秒）                                      │
    │                                                          │
    │  FetchTask → GET /history/{prompt_id}                    │
    │  ParseTaskResult → 解析执行状态                           │
    │    → IN_PROGRESS: 更新进度                               │
    │    → SUCCESS: 存储图像 URL，结算配额                      │
    │    → FAILURE: 退还预扣配额                               │
    └──────────────────────────────────────────────────────────┘

用户轮询
    │
    │  GET /v1/images/comfyui/generations/:task_id
    │
    ▼
返回任务状态 + 图像 URL（直链到 ComfyUI /view 端点）
```

### 2.2 与现有 TaskAdaptor 渠道对比

| 渠道 | ChannelType | 异步模式 | 结果类型 |
|------|-------------|---------|---------|
| Sora | 55 | 提交→轮询 | 视频 URL |
| Kling | 50 | 提交→轮询 | 视频 URL |
| Gemini Task | 24 | 提交→轮询 | 视频/图像 URL |
| **ComfyUI（本方案）** | **58** | **提交→轮询** | **图像 URL** |

ComfyUI 复用完全相同的异步任务框架，仅需实现适配层。

---

## 三、ComfyUI 端配置要求

### 3.1 版本要求

- ComfyUI >= 0.2.0（支持完整的 REST API：`/prompt`、`/history`、`/view`）
- Python >= 3.10
- 建议配合 CUDA 12.x + PyTorch 2.x 使用

### 3.2 启动配置

```bash
# 基础启动（监听所有网卡，允许 new-api 访问）
python main.py --listen 0.0.0.0 --port 8188

# 生产环境建议通过 nginx 添加 API Key 鉴权：
# location / {
#     if ($http_authorization != "Bearer your-secret-key") {
#         return 401;
#     }
#     proxy_pass http://localhost:8188;
# }
```

> **内网部署**：若 ComfyUI 实例在内网且通过防火墙隔离，可不配置鉴权，将渠道的 **密钥（Key）字段留空**即可。适配器检测到 Key 为空时自动省略 `Authorization` 请求头。

### 3.3 导出工作流 API JSON

ComfyUI 有两种 JSON 格式：UI 格式（含界面布局信息）和 **API 格式**（仅含执行逻辑）。new-api 需要 **API 格式**。

**导出步骤：**

1. 在 ComfyUI Web UI 中打开并调试好目标工作流
2. 打开开发者选项：点击右上角齿轮图标 → 勾选 **Enable Dev mode Options**
3. 点击 **Save (API Format)**，下载 `workflow_api.json`

**API JSON 格式示例（SDXL 基础工作流）：**

```json
{
  "4": {
    "class_type": "CheckpointLoaderSimple",
    "inputs": { "ckpt_name": "sd_xl_base_1.0.safetensors" }
  },
  "5": {
    "class_type": "EmptyLatentImage",
    "inputs": { "width": 1024, "height": 1024, "batch_size": 1 }
  },
  "6": {
    "class_type": "CLIPTextEncode",
    "inputs": { "text": "beautiful portrait", "clip": ["4", 1] }
  },
  "7": {
    "class_type": "CLIPTextEncode",
    "inputs": { "text": "blurry, low quality", "clip": ["4", 1] }
  },
  "3": {
    "class_type": "KSampler",
    "inputs": {
      "seed": 42,
      "steps": 25,
      "cfg": 7,
      "sampler_name": "dpmpp_2m",
      "scheduler": "karras",
      "denoise": 1,
      "model": ["4", 0],
      "positive": ["6", 0],
      "negative": ["7", 0],
      "latent_image": ["5", 0]
    }
  },
  "8": {
    "class_type": "VAEDecode",
    "inputs": { "samples": ["3", 0], "vae": ["4", 2] }
  },
  "9": {
    "class_type": "SaveImage",
    "inputs": { "filename_prefix": "ComfyUI", "images": ["8", 0] }
  }
}
```

**需要记录的三个节点 ID：**

| 配置字段 | 含义 | 示例工作流中的节点 |
|---------|------|-----------------|
| `prompt_node_id` | 正向提示词节点（`CLIPTextEncode`，连接正向 conditioning） | `"6"` |
| `seed_node_id` | 采样器节点（`KSampler` 或类似节点，含 `seed` 字段） | `"3"` |
| `output_node_id` | 输出节点（`SaveImage` 或 `PreviewImage`） | `"9"` |

> **识别提示**：在工作流 JSON 中搜索 `"class_type": "CLIPTextEncode"` 找到提示词节点；搜索 `"seed"` 找到采样器节点；搜索 `"SaveImage"` 找到输出节点。若有多个 `CLIPTextEncode`，通过其下游连接判断哪个是正向提示词。

---

## 四、new-api 修改清单

### 4.1 `constant/channel.go` — 新增渠道类型

**修改内容：**

```go
// 在 ChannelTypeCodex = 57 之后插入：
ChannelTypeComfyUI = 58
ChannelTypeDummy   // 哨兵值，顺移为 59
```

在 `ChannelBaseURLs` 切片末尾追加（index 58）：

```go
"",  // 58 ComfyUI（需管理员填写自定义 BaseURL，如 http://10.0.0.1:8188）
```

在 `ChannelTypeNames` map 中添加：

```go
ChannelTypeComfyUI: "ComfyUI",
```

### 4.2 `dto/channel_settings.go` — 扩展渠道配置结构

在文件中新增 `ComfyUIWorkflowConfig` 结构体，并在 `ChannelOtherSettings` 中添加字段：

```go
// ComfyUIWorkflowConfig 存储单个 ComfyUI 工作流的模板配置。
type ComfyUIWorkflowConfig struct {
    // WorkflowJSON 为 ComfyUI API 格式的完整工作流 JSON（字符串形式）。
    WorkflowJSON string `json:"workflow_json"`
    // PromptNodeID 为正向提示词节点的 ID（如 "6"）。
    PromptNodeID string `json:"prompt_node_id"`
    // SeedNodeID 为随机种子节点的 ID（如 "3"），可选。
    SeedNodeID   string `json:"seed_node_id,omitempty"`
    // OutputNodeID 为输出图像节点的 ID（如 "9"）。
    OutputNodeID string `json:"output_node_id"`
}
```

在 `ChannelOtherSettings` 结构体中添加字段：

```go
// ComfyUIWorkflows 存储 ComfyUI 渠道的工作流配置映射。
// key 为工作流别名（与渠道 Models 字段中的模型名称一致），
// value 为对应的工作流模板配置。
ComfyUIWorkflows map[string]ComfyUIWorkflowConfig `json:"comfyui_workflows,omitempty"`
```

### 4.3 新建 `relay/channel/task/comfyui/dto.go`

```go
package comfyui

// promptRequest 对应 ComfyUI POST /prompt 的请求体。
type promptRequest struct {
    Prompt   map[string]any `json:"prompt"`
    ClientID string         `json:"client_id,omitempty"`
}

// promptResponse 对应 ComfyUI POST /prompt 的响应。
type promptResponse struct {
    PromptID   string         `json:"prompt_id"`
    Number     int            `json:"number"`
    NodeErrors map[string]any `json:"node_errors,omitempty"`
    Error      string         `json:"error,omitempty"`
}

// historyEntry 对应 /history/{prompt_id} 中单个任务条目。
// ComfyUI 返回格式：{"<prompt_id>": {"status": {...}, "outputs": {...}}}
type historyEntry struct {
    Status  historyStatus         `json:"status"`
    Outputs map[string]nodeOutput `json:"outputs"`
}

type historyStatus struct {
    // StatusStr 可为 "success" | "error" | "executing" | ""（排队中）
    StatusStr string  `json:"status_str"`
    Completed bool    `json:"completed"`
    Messages  [][]any `json:"messages,omitempty"`
}

type nodeOutput struct {
    Images []imageOutput `json:"images,omitempty"`
}

type imageOutput struct {
    Filename  string `json:"filename"`
    Subfolder string `json:"subfolder"`
    Type      string `json:"type"` // "output" | "temp"
}
```

### 4.4 新建 `relay/channel/task/comfyui/adaptor.go`

```go
package comfyui

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "math/rand"
    "net/http"
    "net/url"
    "strings"

    "github.com/gin-gonic/gin"
    "github.com/pkg/errors"

    "github.com/QuantumNous/new-api/common"
    "github.com/QuantumNous/new-api/dto"
    "github.com/QuantumNous/new-api/model"
    "github.com/QuantumNous/new-api/relay/channel"
    taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
    relaycommon "github.com/QuantumNous/new-api/relay/common"
    "github.com/QuantumNous/new-api/service"
)

// TaskAdaptor 实现 channel.TaskAdaptor 接口，对接 ComfyUI REST API。
type TaskAdaptor struct {
    taskcommon.BaseBilling
    ChannelType int
    apiKey      string
    baseURL     string
    workflows   map[string]dto.ComfyUIWorkflowConfig
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
    a.ChannelType = info.ChannelType
    a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
    a.apiKey = info.ApiKey
    if info.ChannelOtherSettings.ComfyUIWorkflows != nil {
        a.workflows = info.ChannelOtherSettings.ComfyUIWorkflows
    }
}

// ValidateRequestAndSetAction 验证请求并查找工作流配置，存入 context。
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
    if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, "generate"); taskErr != nil {
        return taskErr
    }
    req, err := relaycommon.GetTaskRequest(c)
    if err != nil {
        return service.TaskErrorWrapperLocal(err, "get_task_request_failed", http.StatusInternalServerError)
    }

    wfConfig, ok := a.workflows[info.OriginModelName]
    if !ok {
        return service.TaskErrorWrapperLocal(
            fmt.Errorf("workflow not found for model: %s", info.OriginModelName),
            "workflow_not_found",
            http.StatusBadRequest,
        )
    }
    if wfConfig.WorkflowJSON == "" {
        return service.TaskErrorWrapperLocal(
            fmt.Errorf("workflow_json is empty for model: %s", info.OriginModelName),
            "workflow_config_invalid",
            http.StatusBadRequest,
        )
    }

    c.Set("comfyui_workflow_config", wfConfig)
    c.Set("comfyui_user_prompt", req.Prompt)
    return nil
}

// EstimateBilling 返回 nil，使用模型基础价格按次计费。
func (a *TaskAdaptor) EstimateBilling(_ *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
    return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
    return a.baseURL + "/prompt", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json")
    if a.apiKey != "" {
        req.Header.Set("Authorization", "Bearer "+a.apiKey)
    }
    return nil
}

// BuildRequestBody 将用户 prompt 和随机 seed 注入工作流 JSON。
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, _ *relaycommon.RelayInfo) (io.Reader, error) {
    wfConfigRaw, exists := c.Get("comfyui_workflow_config")
    if !exists {
        return nil, fmt.Errorf("comfyui_workflow_config not found in context")
    }
    wfConfig := wfConfigRaw.(dto.ComfyUIWorkflowConfig)

    userPrompt, _ := c.Get("comfyui_user_prompt")
    prompt, _ := userPrompt.(string)

    // 解析工作流 JSON 为可修改的 map
    var workflow map[string]any
    if err := json.Unmarshal([]byte(wfConfig.WorkflowJSON), &workflow); err != nil {
        return nil, errors.Wrap(err, "failed to parse workflow_json")
    }

    // 注入正向提示词
    if prompt != "" && wfConfig.PromptNodeID != "" {
        if node, ok := workflow[wfConfig.PromptNodeID].(map[string]any); ok {
            if inputs, ok := node["inputs"].(map[string]any); ok {
                inputs["text"] = prompt
            }
        }
    }

    // 注入随机种子，确保每次生成不重复
    if wfConfig.SeedNodeID != "" {
        if node, ok := workflow[wfConfig.SeedNodeID].(map[string]any); ok {
            if inputs, ok := node["inputs"].(map[string]any); ok {
                inputs["seed"] = rand.Int63n(1 << 32)
            }
        }
    }

    data, err := common.Marshal(promptRequest{Prompt: workflow})
    if err != nil {
        return nil, errors.Wrap(err, "marshal prompt request failed")
    }
    return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
    return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse 解析 ComfyUI /prompt 响应，返回 prompt_id 作为上游任务 ID。
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
    body, err := io.ReadAll(resp.Body)
    _ = resp.Body.Close()
    if err != nil {
        return "", nil, service.TaskErrorWrapperLocal(err, "read_response_body_failed", http.StatusInternalServerError)
    }

    if resp.StatusCode != http.StatusOK {
        return "", nil, service.TaskErrorWrapperLocal(
            fmt.Errorf("comfyui status %d: %s", resp.StatusCode, string(body)),
            "upstream_error",
            resp.StatusCode,
        )
    }

    var pResp promptResponse
    if err := common.Unmarshal(body, &pResp); err != nil {
        return "", nil, service.TaskErrorWrapperLocal(err, "unmarshal_response_failed", http.StatusInternalServerError)
    }
    if pResp.Error != "" {
        return "", nil, service.TaskErrorWrapperLocal(
            fmt.Errorf("comfyui error: %s", pResp.Error),
            "comfyui_error",
            http.StatusBadRequest,
        )
    }
    if pResp.PromptID == "" {
        return "", nil, service.TaskErrorWrapperLocal(
            fmt.Errorf("comfyui returned empty prompt_id"),
            "invalid_response",
            http.StatusInternalServerError,
        )
    }

    // 向客户端返回标准任务提交响应
    c.JSON(http.StatusOK, map[string]any{
        "id":         info.PublicTaskID,
        "task_id":    info.PublicTaskID,
        "status":     "submitted",
        "model":      info.OriginModelName,
        "created_at": info.StartTime,
    })
    return pResp.PromptID, body, nil
}

// FetchTask 调用 ComfyUI GET /history/{prompt_id} 查询任务状态。
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
    promptID, _ := body["task_id"].(string)
    if promptID == "" {
        return nil, fmt.Errorf("empty task_id in FetchTask")
    }

    uri := fmt.Sprintf("%s/history/%s", strings.TrimRight(baseUrl, "/"), url.PathEscape(promptID))
    req, err := http.NewRequest(http.MethodGet, uri, nil)
    if err != nil {
        return nil, fmt.Errorf("create fetch request: %w", err)
    }
    req.Header.Set("Accept", "application/json")
    if key != "" {
        req.Header.Set("Authorization", "Bearer "+key)
    }

    client, err := service.GetHttpClientWithProxy(proxy)
    if err != nil {
        return nil, fmt.Errorf("get http client: %w", err)
    }
    return client.Do(req)
}

// ParseTaskResult 解析 /history/{prompt_id} 响应，返回标准 TaskInfo。
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
    taskInfo := &relaycommon.TaskInfo{}

    // 空对象 {} 表示任务还在队列中尚未进入 history
    trimmed := strings.TrimSpace(string(respBody))
    if trimmed == "{}" || trimmed == "" {
        taskInfo.Status = model.TaskStatusQueued
        return taskInfo, nil
    }

    var historyMap map[string]historyEntry
    if err := common.Unmarshal(respBody, &historyMap); err != nil {
        return nil, errors.Wrap(err, "unmarshal history response")
    }
    if len(historyMap) == 0 {
        taskInfo.Status = model.TaskStatusQueued
        return taskInfo, nil
    }

    var entry historyEntry
    for _, v := range historyMap {
        entry = v
        break
    }

    switch entry.Status.StatusStr {
    case "success":
        taskInfo.Status = model.TaskStatusSuccess
        // 构建完整的图像访问 URL（直链到 ComfyUI /view 端点）
        for _, nodeOut := range entry.Outputs {
            if len(nodeOut.Images) > 0 {
                img := nodeOut.Images[0]
                taskInfo.Url = fmt.Sprintf("%s/view?filename=%s&subfolder=%s&type=%s",
                    a.baseURL,
                    url.QueryEscape(img.Filename),
                    url.QueryEscape(img.Subfolder),
                    url.QueryEscape(img.Type),
                )
                break
            }
        }
    case "error":
        taskInfo.Status = model.TaskStatusFailure
        if len(entry.Status.Messages) > 0 {
            taskInfo.Reason = fmt.Sprintf("%v", entry.Status.Messages)
        } else {
            taskInfo.Reason = "comfyui execution error"
        }
    default:
        // "executing" 或空字符串，任务正在运行
        taskInfo.Status = model.TaskStatusInProgress
    }

    return taskInfo, nil
}

func (a *TaskAdaptor) GetModelList() []string  { return []string{} }
func (a *TaskAdaptor) GetChannelName() string  { return "ComfyUI" }
```

### 4.5 `relay/relay_adaptor.go` — 注册 ComfyUI TaskAdaptor

在 `GetTaskAdaptor` 函数的 `switch channelType` 块中添加：

```go
import (
    // 在现有 import 块中追加：
    taskcomfyui "github.com/QuantumNous/new-api/relay/channel/task/comfyui"
)

// 在 switch(channelType) 中添加 case：
case constant.ChannelTypeComfyUI:
    return &taskcomfyui.TaskAdaptor{}
```

### 4.6 `router/video-router.go` — 添加 ComfyUI 路由

在 `SetVideoRouter` 函数末尾添加路由分组：

```go
// ComfyUI 图像生成任务路由
comfyV1Router := router.Group("/v1/images")
comfyV1Router.Use(middleware.RouteTag("relay"))
comfyV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
{
    // 提交工作流生成任务
    comfyV1Router.POST("/comfyui/generations", controller.RelayTask)
    // 查询任务状态与结果
    comfyV1Router.GET("/comfyui/generations/:task_id", controller.RelayTaskFetch)
}
```

> **注意**：`controller.RelayTask` 和 `controller.RelayTaskFetch` 是复用现有视频任务的控制器，无需新建控制器文件。路由 path 中的 `platform` 由 `GetTaskPlatform()` 通过 `ChannelType` 自动推断（返回 `"58"`）。

### 4.7 `web/src/constants/channel.constants.js` — 前端渠道类型

在 `CHANNEL_OPTIONS` 数组末尾添加：

```javascript
{
  value: 58,
  color: 'orange',
  label: 'ComfyUI',
},
```

### 4.8 `web/src/components/table/channels/modals/EditChannelModal.jsx` — 管理 UI

**① 密钥字段提示（在 `type2secretPrompt` 函数中添加）：**

```javascript
case 58:
  return t('comfyui_key_hint', 'ComfyUI API Key（可选，内网部署可留空）');
```

**② 工作流配置区块（在 ComfyUI 渠道类型时显示）：**

在 Coze 渠道配置区块（`inputs.type === 49`）之后添加：

```jsx
{inputs.type === 58 && (
  <Form.Section text="ComfyUI 工作流配置">
    <Banner
      type="info"
      description={
        <span>
          在下方配置工作流别名与模板的映射关系。
          每个别名须与渠道「支持模型」字段中的名称一致。
          格式：<code>{'{"comfyui-portrait": {"workflow_json": "...", "prompt_node_id": "6", "seed_node_id": "3", "output_node_id": "9"}}'}</code>
        </span>
      }
    />
    <Form.Slot label="工作流配置（comfyui_workflows）">
      <TextArea
        value={(() => {
          try {
            const s = inputs.other_settings ? JSON.parse(inputs.other_settings) : {};
            return JSON.stringify(s.comfyui_workflows || {}, null, 2);
          } catch { return '{}'; }
        })()}
        onChange={(val) => {
          try {
            const parsed = JSON.parse(val);
            handleChannelOtherSettingsChange('comfyui_workflows', parsed);
          } catch { /* 输入中途忽略 JSON 解析错误 */ }
        }}
        autosize
        minRows={8}
        placeholder={'{\n  "comfyui-portrait": {\n    "workflow_json": "...",\n    "prompt_node_id": "6",\n    "seed_node_id": "3",\n    "output_node_id": "9"\n  }\n}'}
      />
    </Form.Slot>
  </Form.Section>
)}
```

---

## 五、API 使用方式

### 5.1 提交工作流生成任务

**端点：** `POST /v1/images/comfyui/generations`

**请求：**

```bash
curl -X POST https://your-new-api.com/v1/images/comfyui/generations \
  -H "Authorization: Bearer sk-your-token" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "comfyui-portrait",
    "prompt": "a beautiful woman, studio lighting, 8k photo realistic"
  }'
```

**请求字段：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | ✅ | 工作流别名，须在渠道 Models 字段中登记 |
| `prompt` | string | ✅ | 正向提示词，注入工作流 `PromptNodeID` 节点 |
| `metadata` | object | ❌ | 预留扩展字段（当前版本忽略） |

**响应（HTTP 200）：**

```json
{
  "id": "task_a1b2c3d4e5f6",
  "task_id": "task_a1b2c3d4e5f6",
  "status": "submitted",
  "model": "comfyui-portrait",
  "created_at": 1745200000
}
```

### 5.2 查询任务状态

**端点：** `GET /v1/images/comfyui/generations/{task_id}`

**生成中：**

```json
{
  "id": "task_a1b2c3d4e5f6",
  "status": "in_progress",
  "progress": "30%",
  "model": "comfyui-portrait"
}
```

**生成成功：**

```json
{
  "id": "task_a1b2c3d4e5f6",
  "status": "completed",
  "progress": "100%",
  "model": "comfyui-portrait",
  "metadata": {
    "url": "http://10.0.0.1:8188/view?filename=ComfyUI_00001_.png&subfolder=&type=output"
  }
}
```

**生成失败：**

```json
{
  "id": "task_a1b2c3d4e5f6",
  "status": "failed",
  "progress": "100%",
  "fail_reason": "comfyui execution error: [[\"execution_error\", {...}]]"
}
```

### 5.3 获取图像

任务成功后，`metadata.url` 字段为 ComfyUI `/view` 端点的直链。客户端直接 GET 该 URL 即可下载图像（`Content-Type: image/png`）。

> **安全注意**：若 ComfyUI 实例在内网，客户端无法直接访问 `/view` URL。此时需要在 new-api 端添加图像代理路由，详见第四节 4.6 的扩展说明。如果 ComfyUI 在公网可访问或客户端也在内网，直链方式足够。

---

## 六、计费方案

### 6.1 计费模式

ComfyUI 渠道使用**按次固定计费（PerCallBilling）**模式：

- 任务提交时按模型单价预扣配额
- 任务成功：保持预扣额度（不退不补）
- 任务失败：全额退还预扣配额
- 不在完成时进行差额结算（`EstimateBilling` 返回 nil，`AdjustBillingOnComplete` 继承 `BaseBilling` 默认返回 0）

### 6.2 模型价格配置

在 new-api 管理后台 → **系统设置 → 模型价格** 中，为每个工作流别名设置固定价格：

```json
{
  "comfyui-portrait": {
    "input": 0,
    "output": 0,
    "use_price": true,
    "model_price": 0.02
  },
  "comfyui-landscape": {
    "input": 0,
    "output": 0,
    "use_price": true,
    "model_price": 0.05
  },
  "comfyui-flux-dev": {
    "input": 0,
    "output": 0,
    "use_price": true,
    "model_price": 0.10
  }
}
```

### 6.3 差异化定价建议

| 工作流类型 | 建议单价 | 定价依据 |
|-----------|---------|---------|
| 标准 SD 1.5 工作流（20步） | $0.01/次 | 低显存消耗，速度快 |
| SDXL 工作流（25步） | $0.03/次 | 中等显存，生成质量较高 |
| Flux.1-dev 工作流（28步）| $0.08/次 | 高显存，质量最优 |
| 含 ControlNet 工作流 | +50% 溢价 | 额外模型加载开销 |

---

## 七、多实例调度

### 7.1 原理

new-api 现有的 Ability 表 + 渠道权重系统已完整支持多实例调度，**无需新增任何代码**：

```
Ability 表：
  (model="comfyui-portrait", group="default", channel_id=101, weight=2)
  (model="comfyui-portrait", group="default", channel_id=102, weight=2)
  (model="comfyui-portrait", group="default", channel_id=103, weight=1)

distributor.go → 加权随机选取 → 按 2:2:1 分配流量
```

### 7.2 多节点配置示例

假设 3 台 GPU 节点（2 台 4090，1 台 3090）：

| 渠道名称 | 代理地址（BaseURL） | 支持模型 | 权重 | 优先级 |
|---------|------------------|---------|------|------|
| ComfyUI-GPU1-4090 | http://10.0.0.1:8188 | comfyui-portrait,comfyui-landscape,comfyui-flux | 2 | 0 |
| ComfyUI-GPU2-4090 | http://10.0.0.2:8188 | comfyui-portrait,comfyui-landscape,comfyui-flux | 2 | 0 |
| ComfyUI-GPU3-3090 | http://10.0.0.3:8188 | comfyui-portrait | 1 | 0 |

- `comfyui-portrait` 按 2:2:1 分配至三台机器
- `comfyui-landscape`、`comfyui-flux` 只分配至两台 4090

### 7.3 故障自动切换

new-api 的渠道自动禁用机制（`auto_ban`）会在渠道连续报错时自动将其下线，流量自动转移至其余正常渠道。恢复后在管理后台手动重启或通过定时测试自动恢复。

### 7.4 按用户组差异化路由

通过渠道的 **分组（Group）** 字段，可以为高付费用户组分配专属高性能节点：

```
ComfyUI-Premium-A100 → Group: premium
ComfyUI-Standard-4090 → Group: default
```

用户 token 配置的分组决定其能访问哪些渠道池。

---

## 八、Workflow 配置指南

### 8.1 管理员完整操作步骤

**Step 1：准备工作流 API JSON**

参考第三节 3.3，导出 `workflow_api.json` 并记录三个节点 ID。

**Step 2：在 new-api 创建 ComfyUI 渠道**

进入管理后台 → **渠道** → **添加渠道**，填写：

```
渠道类型：ComfyUI
渠道名称：ComfyUI-GPU1（建议包含机器标识）
代理地址：http://10.0.0.1:8188
密钥：（可选，内网可留空）
支持模型：comfyui-portrait,comfyui-landscape
权重：2
```

**Step 3：配置工作流 JSON**

在渠道编辑页面的 **ComfyUI 工作流配置** 区域填写：

```json
{
  "comfyui-portrait": {
    "workflow_json": "{\"3\":{\"class_type\":\"KSampler\",\"inputs\":{\"seed\":42,\"steps\":25,\"cfg\":7,\"sampler_name\":\"dpmpp_2m\",\"scheduler\":\"karras\",\"denoise\":1,\"model\":[\"4\",0],\"positive\":[\"6\",0],\"negative\":[\"7\",0],\"latent_image\":[\"5\",0]}},\"4\":{\"class_type\":\"CheckpointLoaderSimple\",\"inputs\":{\"ckpt_name\":\"sd_xl_base_1.0.safetensors\"}},\"5\":{\"class_type\":\"EmptyLatentImage\",\"inputs\":{\"width\":1024,\"height\":1024,\"batch_size\":1}},\"6\":{\"class_type\":\"CLIPTextEncode\",\"inputs\":{\"text\":\"beautiful portrait\",\"clip\":[\"4\",1]}},\"7\":{\"class_type\":\"CLIPTextEncode\",\"inputs\":{\"text\":\"blurry, low quality\",\"clip\":[\"4\",1]}},\"8\":{\"class_type\":\"VAEDecode\",\"inputs\":{\"samples\":[\"3\",0],\"vae\":[\"4\",2]}},\"9\":{\"class_type\":\"SaveImage\",\"inputs\":{\"filename_prefix\":\"ComfyUI\",\"images\":[\"8\",0]}}}",
    "prompt_node_id": "6",
    "seed_node_id": "3",
    "output_node_id": "9"
  }
}
```

> **提示**：`workflow_json` 的值是将 `workflow_api.json` 的内容**压缩为单行字符串并 JSON 转义**后的结果。建议使用在线工具（如 [jsonminify.com](https://jsonminify.com)）压缩后，在外层 JSON 中作为字符串值使用。

**Step 4：配置模型价格**

管理后台 → **系统设置 → 模型价格**，参考第六节添加工作流别名的固定单价。

**Step 5：验证**

```bash
# 1. 提交任务
TASK=$(curl -s -X POST https://your-new-api.com/v1/images/comfyui/generations \
  -H "Authorization: Bearer sk-your-token" \
  -H "Content-Type: application/json" \
  -d '{"model": "comfyui-portrait", "prompt": "a beautiful sunset"}' \
  | jq -r '.task_id')

echo "Task ID: $TASK"

# 2. 轮询状态（等待 completed）
while true; do
  STATUS=$(curl -s https://your-new-api.com/v1/images/comfyui/generations/$TASK \
    -H "Authorization: Bearer sk-your-token" | jq -r '.status')
  echo "Status: $STATUS"
  [[ "$STATUS" == "completed" || "$STATUS" == "failed" ]] && break
  sleep 5
done

# 3. 获取图像 URL
curl -s https://your-new-api.com/v1/images/comfyui/generations/$TASK \
  -H "Authorization: Bearer sk-your-token" | jq '.metadata.url'
```

### 8.2 多工作流同一渠道

单个渠道可以配置多个工作流（建议同一台机器上已部署所有相关模型）：

```json
{
  "comfyui-portrait": {
    "workflow_json": "...",
    "prompt_node_id": "6",
    "seed_node_id": "3",
    "output_node_id": "9"
  },
  "comfyui-landscape": {
    "workflow_json": "...",
    "prompt_node_id": "12",
    "seed_node_id": "3",
    "output_node_id": "15"
  }
}
```

渠道 `Models` 字段对应填写：`comfyui-portrait,comfyui-landscape`

### 8.3 注意事项

1. **workflow_json 大小**：完整 Flux 工作流约 20-80KB。渠道 `OtherSettings` 列为 TEXT 类型，无硬性大小限制，但建议保持在 500KB 以内。

2. **跨渠道一致性**：若多台机器支持同一 model 别名，各渠道的工作流功能须一致（可根据硬件调整步数/采样器，但不应改变输入/输出语义）。

3. **节点 ID 稳定性**：ComfyUI 工作流节点 ID 在编辑 UI 中保持固定（由用户添加节点的顺序决定），但在重新导入或使用不同版本工作流时可能变化，需重新确认。

4. **Flux 工作流特殊说明**：Flux 使用 `CLIPTextEncode`（或 `FluxGuidance`）但结构与 SDXL 不同，需仔细检查节点连接关系确认正向提示词节点 ID。

---

## 九、实施顺序

按以下顺序实施，每步可独立合并：

```
Step 1  constant/channel.go
        → 添加 ChannelTypeComfyUI=58，更新 ChannelBaseURLs 和 ChannelTypeNames
        → 无依赖

Step 2  dto/channel_settings.go
        → 添加 ComfyUIWorkflowConfig 结构体和 ComfyUIWorkflows 字段
        → 无依赖

Step 3  relay/channel/task/comfyui/dto.go（新建）
        relay/channel/task/comfyui/adaptor.go（新建）
        → 依赖 Step 1、Step 2

Step 4  relay/relay_adaptor.go
        → 添加 case constant.ChannelTypeComfyUI
        → 依赖 Step 3

Step 5  router/video-router.go
        → 添加 /v1/images/comfyui/* 路由
        → 依赖 Step 4

Step 6  前端修改
        web/src/constants/channel.constants.js
        web/src/components/table/channels/modals/EditChannelModal.jsx
        → 依赖 Step 1（type=58 常量），可与后端并行开发

Step 7  集成测试
        → 依赖 Step 1-6 全部完成
        → 部署真实 ComfyUI 实例进行端到端验证
```

**预估工作量：**

| 步骤 | 工作量 |
|------|------|
| Step 1-2（常量+DTO） | 0.5 天 |
| Step 3（TaskAdaptor 核心） | 1-2 天 |
| Step 4-5（注册+路由） | 0.5 天 |
| Step 6（前端 UI） | 1 天 |
| Step 7（集成测试） | 1 天 |
| **合计** | **4-5 个工作日** |
