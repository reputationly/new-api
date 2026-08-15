# 体验区 1080P 超分链路改造 + MiniMax-H3 Turbo 接入 — 交接文档

> 日期：2026-08-15 · 跨三个仓（new-api / LightX2V / vllm-omni）· 已全部推送
> 修订：2026-08-15 代码复核后订正 §4.1 / §5.3 / §7（原 §4.1 的方向是错的，详见该节说明）
>
> 这份文档写给下一个接手的人（或 agent）。目标是让你**不必重跑我做过的实验**就能继续，
> 并且知道哪些结论是实测的、哪些还只是推断。

---

## 0. 一句话概括

把体验区写死的「1080P = 480P 生成 + 超分」改成运营可配的档位，顺手修了 SeedVR 的进度上报，
并把新出的 MiniMax-H3 ref2v Turbo LoRA 烘焙上线做了 A/B。**代码全部已合并推送，剩下的是
运营配置与几项待验证的压测。**

---

## 1. 三个仓的改动与状态

| 仓 | commit | 内容 | 部署 |
|---|---|---|---|
| new-api | `326c949bb` | 1080P 超分档位可运营化（8 文件，新增 `UpscaleField.jsx`） | ✅ 现网 `v0.13.2-...-38860d14` |
| new-api | `2fd33568b` | 超分档位跟随「自建引擎」开关 + 插帧总闸门关闭 | ✅ |
| new-api | `38860d141` | **修复漏掉的 import**（线上 ReferenceError） | ✅ |
| new-api | `e862cf6a4` | 开启 eslint `no-undef` + `ecmaVersion` 2021，删历史死函数 | ✅ |
| new-api | `2e9111c6a` | EditChannelModal 全文件 prettier 格式化（纯格式） | ✅ |
| LightX2V | `595665fd` | SR 分段进度按本 rank 段序列折算 | ✅ 镜像 `arm64-a100-latest`（2026-08-15 14:00 构建，含此修复） |
| vllm-omni | `3fc71489` | MiniMax-H3 Turbo LoRA 接入手册 | 文档，无需部署 |

### 1.1 new-api 侧改造要点

- **配置结构**：模型级 `upscale: [{to, model, from}]`，挂 `PLAYGROUND_MODEL_LEVEL_FIELDS`，
  与 `pipeline`/`engine`/`defaultSteps` 同层。`from` 留空 = 自动取「小于 to 的最大原生档」。
- **倍率从概念上消失**：引擎按 `min(源面积开方 × sr_ratio, config target 面积开方)` 封顶
  （`seedvr_runner.py:119`），发大只被封顶、发小才掉档，所以前端固定发 `4.0`。
  旧代码写死的 `2.25` 是按标称 854×480 算的，与 wan 实际生成的 832×480 差 1.3%，
  输出落在 1872×1072——**标着 1080P 却不是**。
- **精确 1080**：超分段下发 `resize_mode=fixed_shape`，引擎中心裁到 1920×1080
  （默认 `adaptive` 会因 `DivisibleCrop(16)` 停在 1104）。代价是上下各裁 12 像素。
- **面向用户的措辞统一成「画质增强」**，不再出现「超分」这个模型侧行话（管理端保留）。

### 1.2 运营必须配的两项（不配则功能不生效）

1. `seedvr2` 的**尺寸 / 分辨率 = `1080P`**。它没挂任何玩法 tab，编辑入口在
   **分类页 →「按模型交叉检查」卡片**的孤儿字段区。
2. 目标模型的**「自建引擎」开关打开**。`2fd33568b` 之后，没勾这个开关**超分档位不渲染**。

---

## 2. 实测结论（都有数据，别再重跑）

### 2.1 SeedVR2：3B 优于 7B，已定案

同素材（768p/124帧）、同配置对比：

| | 3B | 7B | 7B-sharp |
|---|---|---|---|
| 耗时 | 152 s | 157 s | 157 s |
| 显存 | 24,955 MiB | 37,743 MiB | 37,743 MiB |
| 梯度能量 / 高频占比 | **4.652 / 0.0275** | 3.918 / 0.0199 | 3.908 / 0.0175 |

- **7B 不是「细节更多」，而是更忠实地保留源的噪声**（含压缩波纹）；3B 抹噪 + 补边缘，
  用户肉眼确认 3B 波纹更少。官方论文（arXiv 2506.05301）也记载 3B 蒸馏版用户偏好度反超 7B，
  且明确警告「轻退化 AIGC 输入易过度生成细节」。
- **PSNR/SSIM 在这个任务上是误导性指标**：它奖励「与源一致」，而超分的目标恰恰是去掉源的缺陷。
- 7B 在 15s 素材上默认段长直接 OOM，压到 seg48 才跑得动（207s→355s）。
- **7B 家族已关闭，不要再评估**，除非输入换成重退化素材（老片、强压缩）。

### 2.2 SeedVR 的四项能力扫描

| 能力 | 结果 |
|---|---|
| 精确 1080（`resize_mode=fixed_shape`） | ✅ 可用，纯请求级参数 |
| **单图超分**（`image_path`） | ✅ 可用，61 s / 显存仅 4.2 GB —— **图像玩法可直接接，尚未接入** |
| 超分 + 插帧（`target_fps=32`） | ✅ 可用（输出 32fps/482 帧），但强制退回串行，362 帧从 207s→673s |
| 多步推理（`infer_steps=2`） | ❌ 无效，scheduler 用自己的 sampling_cfg，单步蒸馏模型没有这个旋钮 |

### 2.3 SR 进度上报修复（LightX2V `595665fd`）

- **根因**：`_run_sr_single_segment` 每段都以 `segment_idx=0` 调用（为了让 `end_run_segment`
  逐段释放 inputs），而 `init_run` 每段把 `video_segment_num` 重置回 1 → 进度恒 100%。
- **表现**：9 段任务前 40 秒 0%，第一段做完直接跳满，再卡到结束。
- **改法**：在 `_run_sr_single_segment` 里包一层回调，按「已完成段数 + 段内进度」重算，
  用 `try/finally` 还原。传的是**本 rank 自己的**段序号（只有 rank 0 能上报，用全局段号会让
  进度停在末尾附近）。
- **实测**（3 段单卡）：0 → 33.33 → 66.67 → 100 ✅

### 2.4 MiniMax-H3 ref2v Turbo4：已烘焙，未采纳

- LoRA：`minimax_h3_ref2v_turbo_4step_v0.1_bf16.safetensors`，**融合缩放 0.0625**
  （rank 128 / alpha 8）。**注意 scale 不按步数走**：同为 4 步的 `fl2v_4step_v1.0_768p`
  是 1.0，差 16 倍。
- 产物：`/nfs-data/models/MiniMax-H3-Ref2VA-Turbo4-BF16`（62 GB，`||delta||/||W||` 中位数 0.0002）
- **A/B 结果（1344×768 / 15s）**：

| | 基座 INT8 20步 | Turbo4 BF16 4步 | Turbo4 BF16 8步 |
|---|---|---|---|
| 耗时 | 1033 s | **240 s (4.30×)** | 432 s (2.39×) |
| 单卡显存峰值 | 38,103 MiB | **40,175 MiB（余量仅 785 MiB）** | 40,121 MiB |
| 码率 | 11,903 kbps | 15,292 kbps | 14,645 kbps |

- **8 步没有价值**：与 4 步互比 PSNR 16.08 / SSIM 0.533，是另一次采样而非「更精细的 4 步」；
  耗时翻倍、码率反降。
- **用户已决定暂时放弃 Turbo4 BF16**（显存余量 1.9%，换个输入就 OOM）。
- 归因提醒：基座是 INT8、turbo 是 BF16，4.30× 里混了量化格式的影响。

---

## 3. 进行中的压测（接手重点）

**目标**：基座 INT8 / 20 步 / 1344×768 / 15s 固定，参考输入从 1 图递增到 9 图 → 3 视频 → 3 音频，
找 OOM 点。

### 3.1 已有数据

| 点 | 状态 | 耗时 | 单卡显存峰值 |
|---|---|---|---|
| 1 图 | ✅ | 1047 s | 38,137 MiB |
| 2 图 | ✅ | 1212 s | 39,603 MiB |
| 3 图 | ✅ | 1370 s | 37,959 MiB |
| 6 图 | ✅ | 2126 s | 39,533 MiB |
| **9 图** | ❌ **OOM** | 20 s | 39,907 MiB |

**结论**：临界在 **7～8 图**之间。OOM 发生在 **20 秒**、还没进 denoise 的编码阶段
（`Tried to allocate 1.11 GiB, 1.08 GiB free`）。耗时随图数增长且在加速
（1→3 图约 +160 s/图，3→6 图约 +252 s/图）。

**注意**：显存峰值并不随图数单调上升（i3 比 i2 还低），因为峰值出现在 VAE 阶段，由输出尺寸决定；
参考图主要影响**编码阶段的瞬时分配**和**耗时**。

### 3.2 待做

1. **补 i7 / i8** 夹出确切临界（每点约 35 分钟）。
2. **验证参考图分辨率的影响** —— 我认为这是最有价值的一步，见 §4。
3. 视频/音频档（i9v1..v3、i9v3a1..a3）：**9 图已 OOM，加视频音频必然更糟，价值不大**，
   除非先解决图的问题。

### 3.3 复现方法

脚本在管理节点（`ssh root@111.172.214.42`）：

```bash
/root/h3scale.sh <tag> <ip:port> <node_ip> <nimg> <nvid> <naud>   # 单点
/root/h3queue.sh <ip:port> <node_ip> "<tag:ni:nv:na> ..."          # 串行队列
```

素材：`/nfs-output/h3_scale/`（img1-9.png 各 1344×768、vid1-3.mp4 768p/4s、aud1-3.m4a）
结果：`/nfs-output/h3_scale/out/`（`_matrix.csv` 汇总、`*.log` 明细、`*.samples` 每 5 秒采样）

---

## 4. 我推断但**尚未验证**的优化方向

> 以下是分析结论，**没有实测数据**，接手时请先验证再采信。

### 4.1 参考图短边被硬编码成 2048（优先级最高，已定位到代码）

> **修订说明（2026-08-15，代码复核后）**：本节初版建议「在 new-api 物化输入时限制参考图尺寸」，
> **那是错的**。`materializeR2VAInputs`（`adaptor.go:911`）只把 URL/base64 原样落 NFS，不解码
> 也不缩放；而服务端会把参考图**重新归一**，所以在 new-api 侧压缩尺寸完全无效。下面是订正后的分析。

**真正的机制**（`pipeline_minimax_h3.py`）：

```python
MINIMAX_H3_REFERENCE_IMAGE_SHORT_EDGE = 2048          # :113
scale = MINIMAX_H3_REFERENCE_IMAGE_SHORT_EDGE / min(width, height)   # :540
prepared_images.append(item.resize((ref_w, ref_h), LANCZOS))         # :1760  ref2va 路径
```

`scale` **没有 `min(1.0, …)` 上限**，所以小图会被**放大**：喂 448×256 得到 `scale = 8`，
最终仍是 3584×2048。参考图的实际尺寸与调用方给什么无关，恒等于「短边 2048」。

**量级核算**（config 里 `vae_ratio: 16`、`patch_size: [1,2,2]`）：

| | 尺寸 | rows |
|---|---|---|
| 单张参考图 | 3584×2048 | `112 × 64 = 7,168` |
| 9 张参考图 | — | **64,512** |
| 目标视频（1344×768 / 362 帧） | — | ~107,856 |

**9 张参考图约占目标序列的 60%**。这与实测的 OOM 位置完全吻合：i9 在 **20 秒**、还没进 denoise
的编码阶段就炸，而 `MiniMaxH3VideoVAE.encode_image`（`vae.py:212`）对单图是**关掉 parallel
tiling 后单卡编码**的。

**还有个不对称值得注意**：参考**视频**用的是 `MINIMAX_H3_BASE_SHORT_EDGE = 768`
（`reference_video.py:21`），唯独参考**图**是 2048，差 2.67 倍。

**建议动作**：把 `MINIMAX_H3_REFERENCE_IMAGE_SHORT_EDGE` 下调（与参考视频对齐到 768），
或做成请求级 / 环境变量可配。这是 **vllm-omni 侧的代码改动**，不是 new-api 的配置项。
改动会影响参考图的信息量，**需要重做画质 A/B**，不能只看 OOM 是否消失。

### 4.2 四张卡为什么没发挥作用（已查清，非配置问题）

四卡显存读数几乎逐字节相同（38137 ×4）——**TP 切的是权重，不切激活**，而 H3 的瓶颈正是
packed sequence 的激活。日志确认 `sp_size=1, ulysses=1, ring=1`，序列并行完全没开。

但**不要去开它**，vllm-omni 的文档已经否决过：

- `docs/实验报告/MiniMax-H3-vLLM-Omni-交接文档.md:16`：「拓扑 `-tp 4`，本轮不再研究 TP2+SP2/USP4」
- **USP/Ulysses 是复制整份 DiT 权重而非切分**（全局方案 :30），宿主内存从 tp4 的 160 GB
  涨到 216 GB，只剩 22–24 GB
- 失败机理：DiT 组的 NCCL 通信子惰性创建时走**裸 `cudaMalloc`**，绕开 PyTorch 缓存分配器，
  在低水位下是掷硬币（usp4 失败 4 次，tp4 十三次没输过）

H3 的 `num_attention_heads = 56`，约束是 `(56 / tp) % ulysses_degree == 0`，tp2sp2 数学上可行，
**但宿主内存扛不住**。出路见全局方案 :647（编码器量化 / 双机 8 卡 / 换 80 GB 卡）。

### 4.3 两处代码级优化（已定位，改动很小）

**① `denoise_loop.py:253` 的 `.float()[mask]` 顺序**

```python
mv_video_t = v_video.float()[update]        # 先给整条序列分配 fp32，再索引
mv_audio_t = v_audio.float()[audio_update]
```

`update` 掩码只覆盖目标帧，但 `.float()` 在索引**之前**执行，会为**整条 packed sequence**
分配一份 fp32 临时副本。按 107,856 rows × 5376 hidden × 4B ≈ **2.2 GB**。改成
`v_video[update].float()` 即可，纯收益、无行为变化。在当前显存余量只有 1～3 GB 的水位下，
这个量级足以决定成败。

**② `condition_noise.py:89` 的 `full_t` 噪声**

```python
full_t = max(target_latent_t + imgvid_cond_num_frames, latent_t)
noise = torch.randn(1, 24, full_t, latent_h, latent_w, ...)[:, :, :latent_t]   # 生成后立刻切掉
```

先按 `full_t` 分配再切到 `latent_t`，浪费真实存在。但它在 **CPU / fp32**、逐个参考项串行分配、
用完即释放，**对 GPU OOM 没有帮助**，只减 CPU 峰值与分配开销。优先级低于上面几条。

### 4.4 参考 token 的 K/V 缓存（理论可行，收益存疑）

`denoise_loop.py:227-290` 每步把整条 packed sequence 过 50 层，而参考行每步被重置回锚点
（`video_rows[~update] = cond_anchor`）——**输入不变却重算 20 遍**。

但不能直接照搬 LLM 的 KV cache：参考行的 K/V 受 `imgvid_cond_timestep = max(t_v, cond_noise_aug)`
调制，**随步数变化**，只有 `max()` 被常数夹住之后才真正不变。而且**这省的是算力不是峰值显存**
（cache 本身也占显存），对 OOM 帮助有限。

---

## 5. 踩过的坑（别重复踩）

1. **`vite build` 不检查未定义变量** —— 打包会把它当全局变量，留到运行时才 `ReferenceError`。
   我因此把一个漏掉的 import 推到了线上。现已开启 eslint `no-undef`（`e862cf6a4`），
   且 `ecmaVersion` 必须 ≥ 2021（否则 `2_000_000` 这种数字分隔符会让整个文件 parse 失败被跳过）。
2. **`--allowed-local-media-path` 必须显式给**。`GPUSTACK_MEDIA_ROOT` 读的是 **worker 进程的
   环境变量**（`vllm_omni.py:366`），在模型实例的 Environment Variables 里配**无效**。
   直接在 backend_parameters 里传 `--allowed-local-media-path=/nfs-output`（先例见
   `lightx2v-节点运维手册.md:383`）。**绝不能填 `/`**。
3. **在 vLLM-Omni H3 上，`DELETE /v1/tasks/{id}` 之后实例卡死（实测现象，根因未定位）**：
   2026-08-15 取消三台的在跑任务后，`10.0.0.90:40058` 与 `10.0.0.24:40039` 持续处于
   `is_processing=false` 但 `active_count=1`，新任务 pending 三分钟不动、显存不释放
   （38.1 / 37.3 GiB），**重启实例才恢复**；同批的 `10.0.0.53:40043`（取消时任务已接近完成）
   正常回落到 18.2 GiB。
   ⚠️ 复核提示：有人引用 LightX2V 的 `task_manager.py` / `api/server.py` 的 `check_stop` +
   `finally` 释放锁来质疑这条——**那是另一个引擎的代码**，本现象出在 vLLM-Omni 的 H3 实例上，
   两者不通用。要否证/定位需查 vllm-omni 的任务生命周期，尚未做。
   生产影响：用户点一次取消，那个实例可能就废了，值得优先定位。
4. **`--omni` 会重复**：GPUStack 的 vLLMOmni backend 自己会加，backend_parameters 里不要再写。
5. **步数是请求级的**，yaml 里的 `num_inference_steps` 只是兜底默认值。turbo 权重不配
   new-api 的 `defaultSteps` 就会跑 20 步，加速全丢且画质劣化。
6. **`scripts/` 在 vllm-omni 的 .gitignore 里**（`:184`），下载脚本不进版本库。
7. **参考图在服务端会被重新归一到短边 2048**，调用方给什么尺寸都一样（见 §4.1）。
   任何「在 new-api 侧压缩参考图来省显存」的想法都是无效的，别再试。
8. **`h3MinDurationSec = 4.0` / `h3MaxDurationSec = 15.0`（`minimax_h3.go:44-45`）是死代码**：
   全 relay 只有定义、没有任何引用，且已落后于 vllm-omni 当前的 `[2.0, 16.0]`
   （`pipeline_minimax_h3.py:136`）。建议直接删掉而不是改数值——留着下次还会漂移。

---

## 6. 资源与产物位置

**节点**
- 计算节点 0030：`ssh -p 43055 root@111.172.214.16`（有 docker + lightx2v 镜像，做 ffprobe/分析）
- 管理节点：`ssh root@111.172.214.42` → 可跳转任意节点（`ssh root@10.0.0.x`）

**产物**
```
/nfs-output/h3_ab/          H3 ref2v A/B（480p/5s 与 768p/15s 各三版 + 参考图）
/nfs-output/h3_scale/       输入规模压测素材与结果
/nfs-data/sr_exp/b7b/       SeedVR 3B vs 7B（动画素材）
/nfs-data/sr_exp/real/      SeedVR 3B vs 7B（真实场景）+ 对比 GRID
/nfs-data/sr_exp/feat/      SeedVR 四项能力扫描（F1 精确1080 / F3 插帧 / F4 单图超分）
/nfs-data/models/MiniMax-H3-Ref2VA-Turbo4-BF16    已烘焙的 turbo 权重（62 GB）
```
本机：`~/Desktop/sr_cmp/`（SeedVR 对比视频，已拉回）

**相关文档**
- `vllm-omni/docs/MiniMax-H3-Turbo-LoRA-接入手册.md` —— 下次上游发新 LoRA 照它做，零镜像构建
- `vllm-omni/docs/实验报告/MiniMax-H3-GPUStack-生产部署档.md` —— env / flag / 为什么是这个值
- `LightX2V/docs/SeedVR2-实验测试报告.md` —— 注意第 40 行「封顶实锤」措辞不准确，
  那条其实证明的是「sr_ratio 字段当时还没加进 schema，恒为默认 2.0」

---

## 7. 建议的接手顺序

> 本节已按 2026-08-15 的代码复核修订。初版把「在 new-api 物化层压缩参考图」列为第一优先级，
> 那是**错的**（§4.1 有订正说明）——真正的杠杆在 vllm-omni 侧。

1. **下调 `MINIMAX_H3_REFERENCE_IMAGE_SHORT_EDGE`**（2048 → 768，与参考视频对齐），
   或做成请求级 / env 可配。**这是把 9 图 OOM 变成可用的最短路径**：9 张图目前占目标序列 60%，
   降到 768 后约减少 7 倍。⚠️ 改动影响参考图信息量，**必须重做画质 A/B**，不能只看 OOM 消失。
2. **修 `denoise_loop.py:253` 的 `.float()[mask]` 顺序**（§4.3①）。一行改动，省约 2.2 GB
   显存峰值，无行为变化。在余量只有 1～3 GB 的水位下值得先做。
3. **回测 BF16 Turbo4**（前两条落地后显存可能已经够）；仍紧再走 **Turbo4 + W8A8 离线量化**
   （`bake_turbo_lora.py` 烘 BF16 → `quantize_minimax_h3_int8.py` 量化）。
   ⚠️ 但注意：基座 INT8（38,103 MiB）与 Turbo4 BF16（40,175 MiB）**只差 2 GB**，而权重差是
   62 GB vs 44 GB——说明显存大头是激活不是权重（权重被 offload 了），**量化的收益可能远小于直觉**。
4. **补 i7 / i8** 夹出确切的图数临界（每点约 35 分钟）。
5. 顺手清理：`condition_noise.py:89` 的 `full_t` 分配（CPU 侧，§4.3②）、new-api 那两个
   未被引用的时长常量（§5.8）。
6. **接单图超分**（§2.2）——同一个部署、同一个模型、61 秒、4.2 GB 显存，图像玩法白捡一个能力，
   只差一个 task_type 分支。与上面几条正交，随时可做。
