import { describe, it, expect } from 'vitest';
import { getPromptCacheSummary } from '../UsageLogsColumnDefs';

// 日志页的「缓存写入」数必须等于后端计费用的那个数：
// 汇总、拆分之和、后端写下的 cache_write_tokens 三者取最大。
// 曾经「有拆分就只看拆分之和」，汇总 50 / 拆分 20+10 时页面只显示 30。
describe('getPromptCacheSummary', () => {
  it('只有拆分时用拆分之和', () => {
    const summary = getPromptCacheSummary({
      cache_creation_tokens_5m: 20,
      cache_creation_tokens_1h: 10,
    });
    expect(summary.cacheWriteTokens).toBe(30);
  });

  it('汇总大于拆分之和时以汇总为准', () => {
    const summary = getPromptCacheSummary({
      cache_write_tokens: 50,
      cache_creation_tokens: 50,
      cache_creation_tokens_5m: 20,
      cache_creation_tokens_1h: 10,
    });
    expect(summary.cacheWriteTokens).toBe(50);
  });

  it('没有缓存信息时返回 null', () => {
    expect(getPromptCacheSummary({})).toBeNull();
    expect(getPromptCacheSummary(null)).toBeNull();
  });
});
