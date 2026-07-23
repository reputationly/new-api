import React, { useState, useEffect, useContext } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  Form,
  Button,
  Select,
  Input,
  InputNumber,
  Switch,
  Tooltip,
  Typography,
  Empty,
} from '@douyinfe/semi-ui';
import { Plus, Trash2 } from 'lucide-react';
import { API, showSuccess, showError } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import {
  MUSIC_CAPABILITIES,
  MUSIC_DEFAULT_MAX_CHARS,
  MUSIC_DEFAULT_REF_AUDIO_MB,
  MUSIC_DEFAULT_VIDEO_MB,
  parseMusicModelConfig,
} from '../../../constants/musicPlayground.constants';

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

export default function SettingsMusicModels(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState, statusDispatch] = useContext(StatusContext);

  const [defaultMaxChars, setDefaultMaxChars] = useState(
    MUSIC_DEFAULT_MAX_CHARS,
  );
  const [defaultRefAudioMaxMB, setDefaultRefAudioMaxMB] = useState(
    MUSIC_DEFAULT_REF_AUDIO_MB,
  );
  const [defaultVideoMaxMB, setDefaultVideoMaxMB] = useState(
    MUSIC_DEFAULT_VIDEO_MB,
  );
  // [{ model, capabilities:[], maxChars, refAudioMaxMB, videoMaxMB }]
  const [modelRows, setModelRows] = useState([]);

  useEffect(() => {
    const cfg = parseMusicModelConfig(props.options?.MusicModelConfig);
    setDefaultMaxChars(cfg.default.maxChars);
    setDefaultRefAudioMaxMB(cfg.default.refAudioMaxMB);
    setDefaultVideoMaxMB(cfg.default.videoMaxMB);
    setModelRows(
      Object.entries(cfg.models || {}).map(([model, c]) => ({
        model,
        capabilities: c.capabilities || [],
        maxChars: c.maxChars,
        refAudioMaxMB: c.refAudioMaxMB,
        videoMaxMB: c.videoMaxMB,
        translationEnabled: c.translation?.enabled === true,
        translationDefaultModel: c.translation?.defaultModel || '',
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
        videoMaxMB: undefined,
        translationEnabled: false,
        translationDefaultModel: '',
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
          videoMaxMB: normInt(r.videoMaxMB),
        };
        // 仅在启用时写 translation,保持 JSON 精简;defaultModel 为选填。
        if (r.translationEnabled) {
          models[name].translation = {
            enabled: true,
            defaultModel: (r.translationDefaultModel || '').trim(),
          };
        }
      });
      const value = JSON.stringify({
        default: {
          maxChars: normInt(defaultMaxChars),
          refAudioMaxMB: normInt(defaultRefAudioMaxMB),
          videoMaxMB: normInt(defaultVideoMaxMB),
        },
        models,
      });
      const res = await API.put('/api/option/', {
        key: 'MusicModelConfig',
        value,
      });
      if (res.data.success) {
        showSuccess(t('保存成功'));
        statusDispatch({
          type: 'set',
          payload: { ...statusState.status, MusicModelConfig: value },
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
        text={t('音乐模型配置')}
        extraText={t(
          '声明哪些是音乐模型并配置约束。勾选了对应能力(文生音乐/音乐改编/音乐重绘/文生音效/视频生音/歌声合成)的模型会出现在音乐体验区对应标签页，能力也会作为标签在模型广场展示。字数上限限制单次描述文本长度(0 表示不限制)；参考音大小上限限制上传驱动/参考/歌声音频(MB)；视频大小上限限制上传源视频(MB，仅视频生音用)。开启「中译英」后,文生音效/视频生音的中文提示词会先用所选默认语言模型翻译成英文再生成(AudioX 文本编码器仅认英文)。留空则用默认值兜底。',
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
          <div style={{ flex: 1, minWidth: 200 }}>
            <Text strong>{t('默认字数上限')}</Text>
            <InputNumber
              min={0}
              value={defaultMaxChars}
              onChange={setDefaultMaxChars}
              placeholder={t('如 2000，0 表示不限制')}
              style={{ width: '100%', marginTop: 8 }}
            />
          </div>
          <div style={{ flex: 1, minWidth: 200 }}>
            <Text strong>{t('默认参考音大小上限(MB)')}</Text>
            <InputNumber
              min={0}
              value={defaultRefAudioMaxMB}
              onChange={setDefaultRefAudioMaxMB}
              placeholder={t('如 20')}
              style={{ width: '100%', marginTop: 8 }}
            />
          </div>
          <div style={{ flex: 1, minWidth: 200 }}>
            <Text strong>{t('默认视频大小上限(MB)')}</Text>
            <InputNumber
              min={0}
              value={defaultVideoMaxMB}
              onChange={setDefaultVideoMaxMB}
              placeholder={t('如 50')}
              style={{ width: '100%', marginTop: 8 }}
            />
          </div>
        </div>

        <Text strong>{t('按模型配置')}</Text>
        <div style={{ marginTop: 8 }}>
          {modelRows.length === 0 ? (
            <Empty
              description={
                <Text type='tertiary'>{t('暂无音乐模型，请添加')}</Text>
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
                  optionList={MUSIC_CAPABILITIES.map((c) => ({
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
                  style={{ flex: 1, minWidth: 120 }}
                />
                <InputNumber
                  min={0}
                  value={row.refAudioMaxMB}
                  onChange={(v) => updateRow(idx, { refAudioMaxMB: v })}
                  placeholder={t('参考音MB')}
                  style={{ flex: 1, minWidth: 120 }}
                />
                <InputNumber
                  min={0}
                  value={row.videoMaxMB}
                  onChange={(v) => updateRow(idx, { videoMaxMB: v })}
                  placeholder={t('视频MB')}
                  style={{ flex: 1, minWidth: 120 }}
                />
                <Tooltip content={t('中文将自动翻译为英文后生成')} position='top'>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 4,
                      flexShrink: 0,
                    }}
                  >
                    <Text type='tertiary' size='small'>
                      {t('中译英')}
                    </Text>
                    <Switch
                      size='small'
                      checked={row.translationEnabled}
                      onChange={(v) =>
                        updateRow(idx, { translationEnabled: v })
                      }
                    />
                  </div>
                </Tooltip>
                <Input
                  value={row.translationDefaultModel}
                  onChange={(v) =>
                    updateRow(idx, { translationDefaultModel: v })
                  }
                  placeholder={t('默认语言模型')}
                  disabled={!row.translationEnabled}
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
