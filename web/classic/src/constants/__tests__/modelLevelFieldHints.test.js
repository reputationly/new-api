import { describe, it, expect } from 'vitest';
import { PLAYGROUND_MODEL_LEVEL_FIELDS } from '../playgroundAdmin.constants';

// 模型级字段的提示文案不能承诺一个控件收不下的值。
//
// 这类错静默且后果很大：模型级 int 走的是 TabPanel 里内联的渲染器（`min={1}`），
// 不是 FieldInput（`min={0}`）。Semi 的 InputNumber 在 blur 时把越界值夹回 min，
// 所以 placeholder 写「0 = 不限」时，运营敲 0、实际存进去的是 1。
//
// 对 maxLongEdge 而言 1 是灾难性的：computeImageSize 按 `cap > 0` 认它是有效护栏，
// 长边缩到 1、再被 floorTo 抬回 align，于是该模型**每个比例每个档位都出 32×32**，
// 请求照发、不报错、日志里也看不出异常。
//
// 清空这类字段的唯一正确姿势是**留空**（写回 null），所以提示里不能出现 0。

// 只匹配「0 作为独立取值」：`0 =` / `0 或` / `0、` / `0 /` / `0 不限`。
// 前面那段 (^|[^0-9]) 是必须的 —— 否则「H3 为 20」「留空=1024」里的 0 会被误判。
const PROMISES_ZERO = /(^|[^0-9])0\s*(=|或|、|\/|不限|表示)/;

describe('模型级 int 字段的提示文案', () => {
  const intFields = Object.entries(PLAYGROUND_MODEL_LEVEL_FIELDS).flatMap(
    ([storeKey, fields]) =>
      (fields || [])
        .filter((f) => f.type === 'int')
        .map((f) => ({ storeKey, ...f })),
  );

  it('确实存在 int 型的模型级字段（否则下面几条是空转）', () => {
    expect(intFields.length).toBeGreaterThan(0);
  });

  it.each(intFields)(
    '$storeKey/$key 的 placeholder 不承诺 0（控件 min=1，会把 0 夹成 1）',
    (f) => {
      expect(f.placeholder || '').not.toMatch(PROMISES_ZERO);
    },
  );

  it.each(intFields)('$storeKey/$key 的 help 不承诺 0', (f) => {
    expect(f.help || '').not.toMatch(PROMISES_ZERO);
  });

  // 正则本身也要守：写得太宽会把「为 20」这类正常文案判成违规，
  // 太窄则漏掉真正的承诺。两个方向各钉一个样本。
  it('正则只认独立的 0，不误伤数字里的 0', () => {
    expect('留空 / 0 = 不限').toMatch(PROMISES_ZERO);
    expect('0 或留空=不限制').toMatch(PROMISES_ZERO);
    expect('留空=引擎族默认（H3 为 20）').not.toMatch(PROMISES_ZERO);
    expect('留空=1024').not.toMatch(PROMISES_ZERO);
    expect('留空=不限').not.toMatch(PROMISES_ZERO);
  });
});
