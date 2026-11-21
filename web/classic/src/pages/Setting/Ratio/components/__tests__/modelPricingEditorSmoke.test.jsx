import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';
import ModelPricingEditor from '../ModelPricingEditor';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k, o) => (o ? k : k),
    i18n: { language: 'zh-CN' },
  }),
}));
vi.mock('../../../../../helpers', () => ({
  API: {
    get: vi.fn().mockResolvedValue({ data: { success: true, data: [] } }),
    put: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  renderQuota: (v) => String(v),
}));
vi.mock('../../../../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => false,
  MOBILE_BREAKPOINT: 768,
}));

// 空态冒烟：没有模型时**整个编辑器必须能渲染**。
// 曾经 timePriceCard 在 return 之前就求值 selectedModel.name，
// 未选模型时抛 TypeError，被 ErrorBoundary 拦成白屏——纯渲染测试就能抓到。
describe('ModelPricingEditor 空态', () => {
  it('没有模型时不抛异常', () => {
    expect(() =>
      render(
        <ModelPricingEditor
          options={{}}
          refresh={vi.fn()}
          timeDraft='{}'
          onTimeDraftChange={vi.fn()}
          saveTimePrice={vi.fn()}
        />,
      ),
    ).not.toThrow();
  });
});
