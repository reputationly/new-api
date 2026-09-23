import React, { useEffect, useState } from 'react';
import { Modal, Select, Typography } from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../../helpers';

const { Text } = Typography;

const OPTION_KEY = 'compute_point_setting.showcase_models';

/**
 * 套餐对比表的展示模型（设计文档 §8.3）。
 *
 * 用户在购买页看到的「每期算力点 ≈ N 秒 / N 张」就按这里选的模型换算。
 * 挑几个客户认得的代表模型即可——选多了表会变长，反而看不出差别。
 */
const ShowcaseModelsModal = ({ visible, onClose, t }) => {
  const [value, setValue] = useState([]);
  const [modelOptions, setModelOptions] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!visible) return;
    setLoading(true);
    Promise.all([API.get('/api/option/'), API.get('/api/models/pricing')])
      .then(([optRes, pricingRes]) => {
        const raw = (optRes.data?.data || []).find(
          (o) => o.key === OPTION_KEY,
        )?.value;
        let parsed = [];
        try {
          parsed = JSON.parse(raw || '[]');
        } catch {
          parsed = [];
        }
        setValue(Array.isArray(parsed) ? parsed : []);
        setModelOptions(
          (pricingRes.data?.success ? pricingRes.data.data || [] : []).map(
            (m) => ({ value: m.model_name, label: m.model_name }),
          ),
        );
      })
      .catch(() => showError(t('加载失败，请重试')))
      .finally(() => setLoading(false));
  }, [visible]);

  const save = async () => {
    setSaving(true);
    try {
      // 数组必须发 JSON：后端对 Slice 走 json.Unmarshal，发 "a,b" 会解析失败后
      // **静默跳过**，表现为提示成功、配置却没变（见 SettingsPoints.jsx 同一处说明）。
      const res = await API.put('/api/option/', {
        key: OPTION_KEY,
        value: JSON.stringify(value),
      });
      if (!res.data?.success) {
        showError(res.data?.message || t('保存失败，请重试'));
        return;
      }
      showSuccess(t('保存成功'));
      onClose();
    } catch {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={t('对比表展示模型')}
      visible={visible}
      onCancel={onClose}
      onOk={save}
      okButtonProps={{ loading: saving, disabled: loading }}
    >
      <Text type='secondary' size='small'>
        {t(
          '用户购买页的套餐对比表会把每期算力点换算成这些模型的可用量（秒数 / 张数 / tokens），随模型调价自动更新。按顺序展示，建议 3~5 个。',
        )}
      </Text>
      <Select
        className='mt-3'
        style={{ width: '100%' }}
        multiple
        filter
        loading={loading}
        value={value}
        onChange={setValue}
        optionList={modelOptions}
        placeholder={t('选择展示模型')}
      />
      <Text type='tertiary' size='small' className='mt-2 block'>
        {t(
          '套餐没有覆盖的模型在对比表里显示「—」；限次权益会自动列出，无需在这里选。',
        )}
      </Text>
    </Modal>
  );
};

export default ShowcaseModelsModal;
