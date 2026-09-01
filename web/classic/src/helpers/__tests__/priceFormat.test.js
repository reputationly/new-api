import { describe, it, expect } from 'vitest';
import { formatPriceWithCeiling } from '../priceFormat';

/**
 * 价格显示的取整。两端共用（手机端 Models.jsx 直接 import 本模块），错了就是两端一起错。
 *
 * 向上取整是有意的：0.0073 显示成 0.00 会让人以为免费，宁可略高。但「略高」必须
 * 来自真实价格，不能来自浮点噪声——model_ratio 在库里只存 12 位小数，舍入方向朝上的
 * 那些模型，误差会被 ×2×汇率 放大到 1e-10 量级，撞上 ceil 就凭空多出 1 分钱。
 */

const RATE = 7.3; // ¥/$，见 points_setting.go 的 QuotaPerUnit / 730
const perM = (modelRatio, completionRatio = 1, groupRatio = 1) =>
  modelRatio * 2 * completionRatio * groupRatio * RATE;

describe('formatPriceWithCeiling 的存储精度容差', () => {
  // DeepSeek-V4-Flash：定价本意 ¥1.00/1M。真值 0.0684931506849315… 存成
  // 0.068493150685（第 12 位向上舍入），×2×7.3 = 1.000000000001
  it('12 位小数的存储误差不会让整数价进位', () => {
    expect(formatPriceWithCeiling(perM(0.068493150685))).toBe('1.00');
    expect(formatPriceWithCeiling(perM(0.068493150685, 2))).toBe('2.00');
  });

  // Kimi-K3 输出价：¥100.01 vs ¥100.00，是全站最扎眼的一个
  it('大额价格同样不受影响', () => {
    expect(formatPriceWithCeiling(perM(1.369863013699, 5))).toBe('100.00');
    expect(formatPriceWithCeiling(perM(1.369863013699, 5, 0.95))).toBe('95.00');
  });

  // 活动主打的自建模型
  it('自建模型的折前折后都准', () => {
    expect(formatPriceWithCeiling(perM(0.205479452055))).toBe('3.00');
    expect(formatPriceWithCeiling(perM(0.205479452055, 1, 0.5))).toBe('1.50');
    expect(formatPriceWithCeiling(perM(0.205479452055, 4))).toBe('12.00');
  });

  // 舍入方向朝下的那一半本来就是对的，不能改坏
  it('原本正确的价格不受影响', () => {
    expect(formatPriceWithCeiling(perM(0.821917808219))).toBe('12.00');
    expect(formatPriceWithCeiling(perM(0.547945205479))).toBe('8.00');
  });
});

describe('formatPriceWithCeiling 的既有语义', () => {
  // 真实高出的部分仍要进位——容差只挡噪声，不能顺手把 ceil 变成 round
  it('真实超出仍然向上取整', () => {
    expect(formatPriceWithCeiling(1.001)).toBe('1.01');
    expect(formatPriceWithCeiling(0.0073)).toBe('0.01');
    expect(formatPriceWithCeiling(0.011)).toBe('0.02');
  });

  // < 0.001 走四舍五入：0.00012 向上取整会显示成 0.01，高出近百倍
  it('极小值四舍五入而不是向上取整', () => {
    expect(formatPriceWithCeiling(0.00012)).toBe('0.00');
    expect(formatPriceWithCeiling(0.0009)).toBe('0.00');
  });

  it('整数值原样显示', () => {
    expect(formatPriceWithCeiling(1)).toBe('1.00');
    expect(formatPriceWithCeiling(0)).toBe('0.00');
  });

  it('非数字返回占位符', () => {
    expect(formatPriceWithCeiling(NaN)).toBe('-');
    expect(formatPriceWithCeiling('abc')).toBe('-');
  });
});
