import React, { useEffect, useState } from 'react';
import { Banner, Radio, RadioGroup } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API } from '../../../helpers';
import ModelPricingEditor from './components/ModelPricingEditor';
import ModelRatioSettings from './ModelRatioSettings';
import TimeSlotTemplates from './components/TimeSlotTemplates';

// 模型定价页。
//
// 分时价的入口不再是一张独立卡片：**时段模板**收成顶部一条折叠栏（全局共用、低频），
// 每个模型的分时价内嵌在右侧「模型计费编辑器」里，跟基础价格一起保存——时段本来就是
// 定价的一部分，分成两处配置会让运营找不到、也容易漏存。
//
// 存储上仍是两个 option key：GroupTimeRatio（时段与各档价目）与 ModelRatio 那一套。
// 「应用更改」会先写 GroupTimeRatio、再写价格；任一步失败都会明确报出是哪一个。
export default function ModelPricingCombined({ options, refresh }) {
  const { t } = useTranslation();
  const [editMode, setEditMode] = useState('visual');

  const rawTime = (options && options.GroupTimeRatio) || '{}';
  const [timeDraft, setTimeDraft] = useState(rawTime);

  useEffect(() => {
    setTimeDraft(rawTime);
  }, [rawTime]);

  // 由编辑器在「应用更改」时调用；没改动就不写，避免无谓覆盖。
  const saveTimePrice = async () => {
    if (timeDraft === rawTime) return;
    const res = await API.put('/api/option/', {
      key: 'GroupTimeRatio',
      value: timeDraft,
    });
    if (!res || !res.data || !res.data.success) {
      throw new Error(
        (res && res.data && res.data.message) || t('保存分时价失败'),
      );
    }
  };

  return (
    <div>
      <div style={{ marginTop: 12, marginBottom: 16 }}>
        <RadioGroup
          type='button'
          size='small'
          value={editMode}
          onChange={(e) => setEditMode(e.target.value)}
        >
          <Radio value='visual'>{t('可视化编辑')}</Radio>
          <Radio value='manual'>{t('倍率（高级）')}</Radio>
        </RadioGroup>
      </div>
      <TimeSlotTemplates value={timeDraft} onChange={setTimeDraft} />
      {editMode === 'visual' ? (
        <ModelPricingEditor
          options={options}
          refresh={refresh}
          timeDraft={timeDraft}
          onTimeDraftChange={setTimeDraft}
          saveTimePrice={saveTimePrice}
        />
      ) : (
        <>
          <Banner
            type='warning'
            bordered
            fullMode={false}
            closeIcon={null}
            style={{ marginBottom: 16 }}
            description={t(
              '高级模式：此处直接编辑后端原始 JSON。模型倍率（ModelRatio）即人民币/百万 tokens 输入价，按次价格（ModelPrice）即人民币/次，与上方「可视化编辑」同一口径；补全、缓存等倍率是相对输入价的无量纲系数。不熟悉 JSON 的话建议使用可视化编辑。',
            )}
          />
          <ModelRatioSettings options={options} refresh={refresh} />
        </>
      )}
    </div>
  );
}
