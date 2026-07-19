import React, { useState, useEffect, useContext } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  Form,
  Button,
  Select,
  Input,
  InputNumber,
  Typography,
  Empty,
} from '@douyinfe/semi-ui';
import { Plus, Trash2 } from 'lucide-react';
import { API, showSuccess, showError } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import {
  AUDIO_CAPABILITIES,
  AUDIO_DEFAULT_MAX_CHARS,
  AUDIO_DEFAULT_REF_AUDIO_MB,
  parseAudioModelConfig,
} from '../../../constants/audioPlayground.constants';

const { Text } = Typography;

// 非负整数或 undefined(空输入不下发,交由默认兜底)。
const normInt = (v) => {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : undefined;
};
const normList = (arr) =>
  Array.isArray(arr)
    ? Array.from(new Set(arr.map((s) => String(s).trim()).filter(Boolean)))
    : [];

export default function SettingsAudioModels(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState, statusDispatch] = useContext(StatusContext);

  const [defaultMaxChars, setDefaultMaxChars] = useState(
    AUDIO_DEFAULT_MAX_CHARS,
  );
  const [defaultRefAudioMaxMB, setDefaultRefAudioMaxMB] = useState(
    AUDIO_DEFAULT_REF_AUDIO_MB,
  );
  // [{ model, capabilities:[], maxChars, refAudioMaxMB }]
  const [modelRows, setModelRows] = useState([]);

  useEffect(() => {
    const cfg = parseAudioModelConfig(props.options?.AudioModelConfig);
    setDefaultMaxChars(cfg.default.maxChars);
    setDefaultRefAudioMaxMB(cfg.default.refAudioMaxMB);
    setModelRows(
      Object.entries(cfg.models || {}).map(([model, c]) => ({
        model,
        capabilities: c.capabilities || [],
        maxChars: c.maxChars,
        refAudioMaxMB: c.refAudioMaxMB,
      })),
    );
  }, [props.options]);

  const addRow = () =>
    setModelRows((prev) => [
      ...prev,
      {
        model: '',
        capabilities: [],
        maxChars: undefined,
        refAudioMaxMB: undefined,
      },
    ]);
  const updateRow = (idx, patch) =>
    setModelRows((prev) =>
      prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)),
    );
  const removeRow = (idx) =>
    setModelRows((prev) => prev.filter((_, i) => i !== idx));

  const onSubmit = async () => {
    setLoading(true);
    try {
      const models = {};
      modelRows.forEach((r) => {
        const name = (r.model || '').trim();
        if (!name) return;
        models[name] = {
          capabilities: normList(r.capabilities),
          maxChars: normInt(r.maxChars),
          refAudioMaxMB: normInt(r.refAudioMaxMB),
        };
      });
      const value = JSON.stringify({
        default: {
          maxChars: normInt(defaultMaxChars),
          refAudioMaxMB: normInt(defaultRefAudioMaxMB),
        },
        models,
      });
      const res = await API.put('/api/option/', {
        key: 'AudioModelConfig',
        value,
      });
      if (res.data.success) {
        showSuccess(t('保存成功'));
        statusDispatch({
          type: 'set',
          payload: { ...statusState.status, AudioModelConfig: value },
        });
        if (props.refresh) await props.refresh();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card>
      <Form.Section
        text={t('语音模型配置')}
        extraText={t(
          '声明哪些是语音模型并配置约束。勾选了对应能力(情感合成/语音合成/双人对话/声音设计)的模型会出现在语音体验区对应标签页，能力也会作为标签在模型广场展示。一个模型可勾选多项。语音合成一个标签页内即覆盖上传克隆/预设音色/多语言方言(Qwen3-TTS/VoxCPM2/CosyVoice3/GLM-TTS/MOSS-TTS-Nano 均勾选语音合成即可)。字数上限限制单次合成文本长度(0 表示不限制)；参考音大小上限限制上传参考音/克隆源的文件大小(MB)。留空则用默认值兜底。',
        )}
      >
        <div
          style={{
            display: 'flex',
            gap: 24,
            marginBottom: 24,
            flexWrap: 'wrap',
          }}
        >
          <div style={{ flex: 1, minWidth: 220 }}>
            <Text strong>{t('默认字数上限')}</Text>
            <InputNumber
              min={0}
              value={defaultMaxChars}
              onChange={setDefaultMaxChars}
              placeholder={t('如 2000，0 表示不限制')}
              style={{ width: '100%', marginTop: 8 }}
            />
          </div>
          <div style={{ flex: 1, minWidth: 220 }}>
            <Text strong>{t('默认参考音大小上限(MB)')}</Text>
            <InputNumber
              min={0}
              value={defaultRefAudioMaxMB}
              onChange={setDefaultRefAudioMaxMB}
              placeholder={t('如 10')}
              style={{ width: '100%', marginTop: 8 }}
            />
          </div>
        </div>

        <Text strong>{t('按模型配置')}</Text>
        <div style={{ marginTop: 8 }}>
          {modelRows.length === 0 ? (
            <Empty
              description={
                <Text type='tertiary'>{t('暂无语音模型，请添加')}</Text>
              }
              style={{ padding: '16px 0' }}
            />
          ) : (
            modelRows.map((row, idx) => (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  gap: 8,
                  alignItems: 'flex-start',
                  marginBottom: 12,
                  flexWrap: 'wrap',
                }}
              >
                <Input
                  value={row.model}
                  onChange={(v) => updateRow(idx, { model: v })}
                  placeholder={t('模型名称')}
                  style={{ width: 200, flexShrink: 0 }}
                />
                <Select
                  multiple
                  filter
                  value={row.capabilities}
                  optionList={AUDIO_CAPABILITIES.map((c) => ({
                    label: t(c),
                    value: c,
                  }))}
                  onChange={(v) => updateRow(idx, { capabilities: v })}
                  placeholder={t('支持能力')}
                  style={{ flex: 1, minWidth: 160 }}
                />
                <InputNumber
                  min={0}
                  value={row.maxChars}
                  onChange={(v) => updateRow(idx, { maxChars: v })}
                  placeholder={t('字数上限')}
                  style={{ flex: 1, minWidth: 140 }}
                />
                <InputNumber
                  min={0}
                  value={row.refAudioMaxMB}
                  onChange={(v) => updateRow(idx, { refAudioMaxMB: v })}
                  placeholder={t('参考音MB')}
                  style={{ flex: 1, minWidth: 140 }}
                />
                <Button
                  type='danger'
                  theme='borderless'
                  icon={<Trash2 size={16} />}
                  onClick={() => removeRow(idx)}
                />
              </div>
            ))
          )}
          <Button
            theme='outline'
            type='tertiary'
            icon={<Plus size={16} />}
            onClick={addRow}
            style={{ marginTop: 4 }}
          >
            {t('添加模型')}
          </Button>
        </div>

        <div style={{ marginTop: 24 }}>
          <Button type='primary' onClick={onSubmit} loading={loading}>
            {t('保存设置')}
          </Button>
        </div>
      </Form.Section>
    </Card>
  );
}
