import { describe, it, expect } from 'vitest';
import { aggregateQueue } from '../useImageGeneration';

// 一条图像消息可能是 N 个独立任务，各排在不同实例的队列上。整条消息以最慢的那个为准。
//
// ⚠️ 说不准时必须**显式**回 undefined，不能回空对象：patchConvMessage 是浅合并
// （{ ...m, ...patch }），键不存在就保留上一轮的值 —— 于是正好在"该回落到通用
// 「任务排队中…」文案"的场景，卡片反而冻结在上一次那个数字，队伍看起来卡住不动。
describe('aggregateQueue', () => {
  // 排队中的任务；running 的另起 r()，两者在本地都是 pending，只有 queued 有区别。
  const t = (queueAhead, queueEtaSeconds) => ({
    queued: true,
    queueAhead,
    queueEtaSeconds,
  });
  const running = (queueAhead = 0, queueEtaSeconds = 0) => ({
    queued: false,
    queueAhead,
    queueEtaSeconds,
  });

  it('全部有值 → 取最大（整条消息等最慢的那个）', () => {
    expect(aggregateQueue([t(1, 60), t(3, 200)])).toEqual({
      queueAhead: 3,
      queueEtaSeconds: 200,
    });
  });

  it('任一任务的排队位说不准 → 两个键都显式回 undefined', () => {
    const r = aggregateQueue([t(2, 60), t(undefined, 60)]);
    expect(r).toEqual({ queueAhead: undefined, queueEtaSeconds: undefined });
    // 键必须存在，否则浅合并时保留旧值 —— 这正是回归点
    expect('queueAhead' in r).toBe(true);
    expect('queueEtaSeconds' in r).toBe(true);
  });

  it('没有待办任务 → 同样显式回 undefined', () => {
    const r = aggregateQueue([]);
    expect('queueAhead' in r).toBe(true);
    expect(r.queueAhead).toBeUndefined();
  });

  // 门面在"调度中/无运行实例"时回 queue_ahead: null，这是最常见的说不准。
  it('null 也算说不准', () => {
    expect(aggregateQueue([t(null, null)]).queueAhead).toBeUndefined();
  });

  // 排队位有值、但预计时间说不准：位置照报，时间留空（下游文案会只显示位置）。
  it('只有预计时间说不准时，排队位仍然报', () => {
    const r = aggregateQueue([t(2, 60), t(1, undefined)]);
    expect(r.queueAhead).toBe(2);
    expect(r.queueEtaSeconds).toBeUndefined();
  });

  // 门面对运行中的任务按设计回 queue_ahead: 0（gpustackplus TestRunningTaskKeepsZeroAhead）。
  // 把它算进来 formatQueueHint 会读成「即将开始…」，在整个出图过程中盖掉 loading 态，
  // 界面写着「还没开始」而图正在出 —— 视频/语音/音乐都用 status === QUEUED 门控，
  // 图片这条链路 queued 与 in_progress 同为 pending，只能靠这里筛。
  it('全部已开跑 → 不报排队（否则整个生成期间显示「即将开始…」）', () => {
    const r = aggregateQueue([running(), running()]);
    expect(r.queueAhead).toBeUndefined();
    expect('queueAhead' in r).toBe(true);
  });

  // 反过来也不能矫枉过正：先跑起来一张，不该把另外几张真实的排队位置一起抹掉。
  it('混合时只按仍在排队的那几个算，已开跑的那张不参与', () => {
    const r = aggregateQueue([running(), t(3, 200), t(1, 60)]);
    expect(r.queueAhead).toBe(3);
    expect(r.queueEtaSeconds).toBe(200);
  });
});
