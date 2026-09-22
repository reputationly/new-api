import React, { useMemo } from 'react';
import { Select, Typography } from '@douyinfe/semi-ui';
import {
  previewComputePoints,
  formatPreviewAmount,
} from '../../../../helpers/computePointPreview';

const { Text } = Typography;

/**
 * 每期算力点的换算预览。模型由运营自选——写死几个模型名的话，
 * 在没有那些模型的部署上预览会莫名其妙空掉。
 */
const ComputePointPreview = ({ points, models = [], value, onChange, t }) => {
  const record = useMemo(
    () => models.find((m) => m.model_name === value),
    [models, value],
  );
  const result = useMemo(
    () => previewComputePoints(points, record),
    [points, record],
  );

  return (
    <div className='mt-2'>
      <Text size='small' type='secondary'>
        {t('换算预览')}
      </Text>
      <div className='mt-1 flex items-center gap-2 flex-wrap'>
        <Select
          filter
          showClear
          value={value}
          onChange={onChange}
          placeholder={t('选择模型查看能用多少')}
          style={{ minWidth: 220 }}
          optionList={models.map((m) => ({
            value: m.model_name,
            label: m.model_name,
          }))}
        />
        {result.kind === 'unknown' ? (
          <Text size='small' type='tertiary'>
            {t(result.reason)}
          </Text>
        ) : (
          <Text size='small'>
            ≈ {formatPreviewAmount(result.amount)} {t(result.unit)}
          </Text>
        )}
      </div>
      <Text size='small' type='tertiary'>
        {t('按未打分组折扣的列表价估算，仅供定价参考')}
      </Text>
    </div>
  );
};

export default ComputePointPreview;
