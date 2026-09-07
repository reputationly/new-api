import { describe, it, expect } from 'vitest';
import {
  getMaxEditImagesForModel,
  parseImageSizeConfig,
  IMAGE_MAX_EDIT_IMAGES,
  IMAGE_EDIT_IMAGES_CEILING,
} from '../imagePlayground.constants';
import { recomputeModelLevel } from '../playgroundAdmin.constants';

// 图生图底图张数：改造前全模型写死 3，现在按模型配（体验区管理的「图生图」格）。
// 服务端读同一份配置（common.ImageMaxEditImagesForModel），所以这里的取值链要和
// common/image_edit_images_test.go 的断言一一对得上。

const cfg = (models) => parseImageSizeConfig(JSON.stringify({ models }));

describe('getMaxEditImagesForModel', () => {
  it('未配 → 内置默认 3（= 改造前的行为，未配模型一字不变）', () => {
    const c = cfg({ m: { sizes: ['1024x1024'] } });
    expect(getMaxEditImagesForModel(c, 'm', 'image2image')).toBe(
      IMAGE_MAX_EDIT_IMAGES,
    );
    expect(getMaxEditImagesForModel(c, '配置里没有的模型', 'image2image')).toBe(
      IMAGE_MAX_EDIT_IMAGES,
    );
  });

  it('配了就按配置（sensenova-u1.5 = 1）', () => {
    const c = cfg({
      'sensenova-u1.5': { tabs: { image2image: { maxEditImages: 1 } } },
    });
    expect(getMaxEditImagesForModel(c, 'sensenova-u1.5', 'image2image')).toBe(
      1,
    );
  });

  // 12 必须大于天花板才谈得上钳制。天花板跟着门面调过一次（5→9），那次如果这里恰好
  // 写的是新天花板的值，用例会静默退化成「不测钳制」也照样绿——所以先断言前提成立。
  it('只能收窄：配得比门面天花板大无效', () => {
    expect(IMAGE_EDIT_IMAGES_CEILING).toBeLessThan(12);
    const c = cfg({ m: { tabs: { image2image: { maxEditImages: 12 } } } });
    expect(getMaxEditImagesForModel(c, 'm', 'image2image')).toBe(
      IMAGE_EDIT_IMAGES_CEILING,
    );
  });

  it('0 抬回 1：底图是图生图的唯一输入，0 会把玩法变成死胡同', () => {
    const c = cfg({ m: { tabs: { image2image: { maxEditImages: 0 } } } });
    expect(getMaxEditImagesForModel(c, 'm', 'image2image')).toBe(1);
  });

  it('tab-only：模型级与别的 tab 都不算数', () => {
    expect(
      getMaxEditImagesForModel(
        cfg({ m: { maxEditImages: 1 } }),
        'm',
        'image2image',
      ),
    ).toBe(IMAGE_MAX_EDIT_IMAGES);
    expect(
      getMaxEditImagesForModel(
        cfg({ m: { tabs: { text2image: { maxEditImages: 1 } } } }),
        'm',
        'image2image',
      ),
    ).toBe(IMAGE_MAX_EDIT_IMAGES);
  });
});

// parseImageSizeConfig 的 tab 子层是**白名单式重建**：漏掉一个键，管理页每保存一次
// 就把运营刚配的值删一次（配置文件里对 engine / aspectRatios / optimizePrompt 都记过
// 这一笔）。这条用例就是钉死 maxEditImages 不会重蹈覆辙。
describe('保存往返不丢配置', () => {
  it('parse 保留 tabs.image2image.maxEditImages', () => {
    const parsed = cfg({ m: { tabs: { image2image: { maxEditImages: 2 } } } });
    expect(parsed.models.m.tabs.image2image.maxEditImages).toBe(2);
  });

  it('parse → 再 parse 一次，值仍在（模拟管理页反复保存）', () => {
    const once = cfg({ m: { tabs: { image2image: { maxEditImages: 2 } } } });
    const twice = parseImageSizeConfig(
      JSON.stringify({ default: once.default, models: once.models }),
    );
    expect(twice.models.m.tabs.image2image.maxEditImages).toBe(2);
  });

  it('recomputeModelLevel 不把它反推到模型级（反推上去会被 parse 丢掉）', () => {
    const out = recomputeModelLevel('ImageModelSizeConfig', {
      capabilities: ['图生图'],
      tabs: { image2image: { maxEditImages: 2 } },
    });
    expect(out.maxEditImages).toBeUndefined();
  });
});
