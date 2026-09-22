import { describe, it, expect } from 'vitest';
import {
  emptyEntitlement,
  toEditableEntitlements,
  validateEntitlements,
} from '../EntitlementEditor';

const t = (s) => s;

describe('toEditableEntitlements', () => {
  it('空输入给空数组', () => {
    expect(toEditableEntitlements(undefined)).toEqual([]);
    expect(toEditableEntitlements(null)).toEqual([]);
    expect(toEditableEntitlements([])).toEqual([]);
  });

  // 「不消耗算力点」是无限制模型这个功能本身。用 || 兜底会把 false 改回 true，
  // 和后端那个 GORM default 标签陷阱是同一个 bug 的前端版本。
  it('consume_points=false 必须原样保留', () => {
    const [ent] = toEditableEntitlements([
      { id: 7, models: 'qwen3-flash', consume_points: false },
    ]);
    expect(ent.consume_points).toBe(false);
  });

  it('后端没给 consume_points 时默认按消耗处理', () => {
    const [ent] = toEditableEntitlements([{ id: 1, models: 'a' }]);
    expect(ent.consume_points).toBe(true);
  });

  // 折扣系数同理：0 是非法值该被后端拦下，但不能在这里被 || 静默改成 1
  // 而让运营以为自己填的值生效了。
  it('折扣系数缺失才补 1，显式 0 保留下来交给校验', () => {
    expect(toEditableEntitlements([{ models: 'a' }])[0].consume_discount).toBe(
      1,
    );
    expect(
      toEditableEntitlements([{ models: 'a', consume_discount: 0 }])[0]
        .consume_discount,
    ).toBe(0);
    expect(
      toEditableEntitlements([{ models: 'a', consume_discount: 0.5 }])[0]
        .consume_discount,
    ).toBe(0.5);
  });

  it('保留 id 以便后端按增量比对，不丢失存量权益', () => {
    const [ent] = toEditableEntitlements([{ id: 42, models: 'a' }]);
    expect(ent.id).toBe(42);
  });
});

describe('validateEntitlements', () => {
  it('全部合规时返回空串', () => {
    expect(
      validateEntitlements(
        [
          {
            models: 'gpt-5',
            consume_points: true,
            consume_discount: 1,
            limit_count: 500,
          },
          {
            models: 'qwen3-*',
            consume_points: true,
            consume_discount: 1,
            rate_limit_rpm: 60,
          },
        ],
        t,
      ),
    ).toBe('');
  });

  it('模型范围为空要报错并指出第几条', () => {
    const msg = validateEntitlements(
      [
        {
          models: '  ',
          consume_points: true,
          consume_discount: 1,
          limit_count: 10,
        },
      ],
      t,
    );
    expect(msg).toContain('权益 1');
  });

  it('不限次且没填 RPM 要报错', () => {
    const msg = validateEntitlements(
      [
        {
          models: 'a',
          consume_points: true,
          consume_discount: 1,
          limit_count: 10,
        },
        {
          models: 'b',
          consume_points: true,
          consume_discount: 1,
          limit_count: 0,
        },
      ],
      t,
    );
    expect(msg).toContain('权益 2');
    expect(msg).toContain('速率限制');
  });

  it('不消耗算力点且没填 RPM 要报错，即使有次数上限', () => {
    const msg = validateEntitlements(
      [
        {
          models: 'a',
          consume_points: false,
          consume_discount: 1,
          limit_count: 100,
        },
      ],
      t,
    );
    expect(msg).toContain('速率限制');
  });

  it('折扣系数为 0 要报错', () => {
    const msg = validateEntitlements(
      [
        {
          models: 'a',
          consume_points: true,
          consume_discount: 0,
          limit_count: 10,
        },
      ],
      t,
    );
    expect(msg).toContain('折扣系数');
  });
});

describe('emptyEntitlement', () => {
  // 新建的空权益必须能直接通过后端校验以外的结构要求：id=0 让后端认作新增，
  // 默认消耗算力点、不限次——不限次意味着运营必须填 RPM，由校验去提醒。
  it('新建项的默认值', () => {
    const ent = emptyEntitlement();
    expect(ent.id).toBe(0);
    expect(ent.consume_points).toBe(true);
    expect(ent.consume_discount).toBe(1);
    expect(ent.limit_count).toBe(0);
    expect(ent.rate_limit_rpm).toBe(0);
  });
});
