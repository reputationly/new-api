# 提示词增强档位对照（none / low / high）

2026-11-21 用来决定「各模式该不该开思考、开哪一档」的实测脚本与用例。**只看提示词输出**，不跑生成。

## 用法

```bash
# 需要 MaaS key（脚本读环境变量里的常量，跑前改掉或 export）
python3 scripts/enhance-tiers/pe_tiers.py scripts/enhance-tiers/cases_qwen_pe.json /tmp/rec/qwen  none,low,high
python3 scripts/enhance-tiers/pe_tiers.py scripts/enhance-tiers/cases_music3.json /tmp/rec/music3 none,low,high
python3 scripts/enhance-tiers/pe_tiers.py scripts/enhance-tiers/cases_acestep.json /tmp/rec/ace   none,low,high

# 结构化统计（延迟 / 长度 / JSON 合格 / 三段齐全 / 中文泄漏 / BPM 出现次数）
python3 scripts/enhance-tiers/chk_tiers.py
```

脚本从仓库里读生产同形的提示词：`service/qwenpe/system_prompt_*.txt` + `qwenPEEscapeRule`（qwen_pe）、
`service/aggregate_enhance_music.go` 的 `defaultMusic3EnhanceTemplate`（Music3）、
`service/acestep/ace_caption_v1.txt`（ACE-Step），所以复跑出来和线上发出去的是同一份。

> 想改 `REPO` 指向：脚本默认指向 `<repo>/.wtp`（一个从 main 建的 worktree），改脚本顶部的 `REPO` 常量即可。

## 结果（2026-11-21，各 6–7 条用例）

### 生图 `qwen_pe`

| 档位 | JSON 合格 | 致命样本 | 延迟 |
| --- | --- | --- | --- |
| none | 5/7 | 1 条跑飞（13910 字符，不合法 JSON） | 2.8–27.7s，p50 ≈ 5s |
| **low** | 6/7 | 0 | 9.0–14.0s，很稳 |
| high | 4/7 | 2 条空正文 + 1 条 504（300s） | 3.4–57.6s |

### 生音乐 `MiniMax-Music3`

| 档位 | 三段结构 | 歌词泄漏 | 长度 | 延迟 |
| --- | --- | --- | --- | --- |
| **none（现状）** | 6/6 | 无 | 2160–2885 | 9.8–38.9s |
| low | 6/6 | 无 | 1612–2523 | 15–51s |
| high | 6/6 | 无 | 1278–1734（被压） | 50–186s |

### 生音乐 `ACE-Step`

| 档位 | 格式（单行逗号分隔） | 长度 | 延迟 |
| --- | --- | --- | --- |
| none | ❌ 写成散文段落 | 359–1187 | 2.7–22.5s |
| **low** | ✅ 正确的 tag 列表 | 316–635 | 8.2–30.7s |
| high | ✅ 但过短 | 192–348 | 17–50s |

## 结论

- ACE-Step **必须 low**（格式级差异）；qwen_pe 用 low（更稳）；Music3 维持 none；所有模式都不建议 high。
- high 之所以把正文挤没：vLLM 的 `max_tokens` 是「思考 + 正文」的总量，而 text / qwen_pe 原先不发这个字段。
  这次改动（见 PR）给这两条路补上了 `max_tokens`，档位才真正可用。
- 注意：单次采样方差大，本表是每格 6–7 条用例的结果，不是 3 次重复的稳定性数据；
  稳定性那套方法与阈值见 `service/h3v20/NOTES.md`（视频/图片那两条线做过 3 次重复）。