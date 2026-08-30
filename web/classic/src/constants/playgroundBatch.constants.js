// 「一次生成几张/几条」——图像与视频体验区共用。
//
// 目的不是"批量产出",而是**给不同 seed 多生成几个候选让用户挑**。所以 seed 是这个
// 功能的核心,不是附属参数:N 个请求若 seed 相同,出来的就是 N 份一样的东西。
//
// 实现上是**前端并发发 N 次**,不是给引擎发 batch_size:门面的任务契约是单产物
// (一个 task 一个 save_result_path),引擎侧真出多份也只有第一份会被搬走。
// 音乐页当初也是这么处理的(见 useMusicGeneration 里 ACE-Step 的 batch_size=1 注释)。
//
// ⚠️ **并发会撞 gpustack 的准入控制**。routes/videos.py::_check_admission 按
//   est_wait = (该模型非终态任务数 // 实例数) × 单次延迟
// 估算排队时间,超过容忍值直接 429。按默认容忍值(image 25s / video 150s)算:
//   Krea2(14s)第 3 个就 429;Ideogram-4(45s)**第 2 个**就 429;
//   LTX-2.5 标准 5 秒档(25s)能排到第 7 个。
// 所以要放开 N>2,运营必须把该模型的 lightx2v_model_queue_wait_seconds 调到
// 约 N × 单次延迟。这是配置不是代码,漏配的表现是"选了 3 张,回来 2 张 + 1 个 429"。
// 只影响自建 gpustackplus 模型;第三方渠道(Sora / gpt-image 等)不走这条准入,
// 但有各自的速率限制。
//
// 计费按次:N 张 = N 次,没有批量价。

// 图像与视频共用这一套档位。
//
// **上限取 3 而不是 4**:视频侧的 VIDEO_MAX_CONCURRENT_TASKS = 3 是唯一的按用户
// 在途任务数限制(后端一个都没有,见该常量处的注释)。取 3 时一批正好填满那道护栏、
// 不越界;取 4 就得抬闸,那是改容量政策而不是加个下拉。图像侧本可以更多(同步接口、
// 约 14 s、不占轮询槽),但没必要为此让两端档位不一致 —— 用户在两个页面看到同一组
// 选项,比图像多一档更值。
export const PLAYGROUND_BATCH_COUNTS = [1, 2, 3];

// 默认 1 = 完全保持改造前的行为。多花钱的事不该是默认值。
export const PLAYGROUND_BATCH_DEFAULT = 1;

// 老会话没有这个字段、或存了脏值 → 一律回落 1,与改造前行为一致。
export const normalizeBatchCount = (v) => {
  const n = parseInt(v, 10);
  return PLAYGROUND_BATCH_COUNTS.includes(n) ? n : PLAYGROUND_BATCH_DEFAULT;
};

// seed 取值域:各引擎口径不一(有的 uint32、有的 int64),取 32 位正整数这个公共安全区。
// 0 不用 —— 部分引擎把 0 当"未指定"。
//
// **导出给面板当输入框上界用**:同一个不变量不能在两处各写一个数字。此前 randomSeed
// 与 deriveSeeds 守着这个区间,而面板的种子输入框只有 min={0}、没有上界 —— 用户填得
// 进 2147483646,再选多张,递增就溢出 int32(检视抓到的正是这条)。
export const SEED_MAX = 2147483646;

export const randomSeed = () => Math.floor(Math.random() * SEED_MAX) + 1;

// 把任意整数回卷进 [1, SEED_MAX]。取模前先减 1、之后再加 1,是为了让区间从 1 开始
// 而不是 0(0 被部分引擎当作"未指定")。两次取模是为了处理负数。
const wrapSeed = (v) =>
  ((((Math.floor(v) - 1) % SEED_MAX) + SEED_MAX) % SEED_MAX) + 1;

// 一次生成 n 个候选要用的 seed 列表。
//
// 用户填了 seed → 从它开始递增(seed, seed+1, …)。**不是 n 个都用同一个**:那样
// 出来的是 n 份一样的东西,等于白花 n 倍的钱;而"我填了 seed 就想要这一个确定结果"
// 的诉求,用户把张数选回 1 即可表达,不需要在这里替他决定。
// 递增而非再随机,是为了让这一组同样可复现:记下起始 seed 就能把整组重放出来。
//
// 用户留空 → 前端抽 n 个随机 seed **并下发**(而不是不发、让引擎自己随机)。
// 这一步是这个功能好不好用的关键:seed 由前端定,才能把每个候选的 seed 显示出来,
// 用户看中哪个就能拿着它复现、微调。不发的话"多生成几个让用户选"只完成了一半——
// 选中了却回不去。
// 这里**再归一一次**,尽管调用方通常已经归一过。不是冗余:n 直接决定并发出去几个
// 请求,只兜"是不是正整数"的话,一个脏值 99 就是 99 路并发打出去。档位白名单是这条
// 路径上最后一道闸,便宜且必要。
export const deriveSeeds = (baseSeed, count) => {
  const n = normalizeBatchCount(count);
  const base = Number(baseSeed);
  if (baseSeed !== '' && baseSeed != null && Number.isFinite(base)) {
    // 递增会越过上界:用户填 2147483646、选 3 张 → 末位 2147483648 溢出 int32,
    // 而面板上的种子输入框只有 min={0}、**没有上界**,这个值真填得进去。
    // 用回卷而不是截断到 SEED_MAX —— 截断会让末几个 seed 撞成同一个,
    // 正好毁掉"多张 = 多个不同候选"这件事本身,而且不报错。
    // 区间内的 base 不受影响(wrap(base) === base),只有越界时才动。
    return Array.from({ length: n }, (_, i) => wrapSeed(base + i));
  }
  // 同一批内去重:随机撞车概率极低,但撞了就是两个一模一样的候选,白花一次钱。
  const seeds = [];
  while (seeds.length < n) {
    const s = randomSeed();
    if (!seeds.includes(s)) seeds.push(s);
  }
  return seeds;
};
