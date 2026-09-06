import React, { useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Form,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';

const { Text } = Typography;

// 聚合(编排)模型配置。一个对外模型名 = 一条固定流水线:
//   图片: 提示词增强 → 生成
//   视频: 提示词增强 → 生成 → 超分
//
// 配置本身是一份 JSON(与 VideoModelConfig 等同一套存取惯例)。这里刻意**不做**逐字段
// 的可视化表单:流水线的段数与字段随玩法而变,表单化之后每加一种 stage 就要改一次 UI,
// 而运营真正需要的是"配完能立刻知道对不对" —— 那由下面的「干跑校验」提供,
// 它把配置放进当前站点的模型/分组/能力现状里对一遍,零成本、秒回。
const SettingsAggregateModel = ({ options, refresh }) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(false);
  const [results, setResults] = useState(null);
  const [draft, setDraft] = useState(null);

  const raw = draft ?? options['AggregateModelConfig'] ?? '';

  const submit = async () => {
    try {
      setLoading(true);
      const res = await API.put('/api/option/', {
        key: 'AggregateModelConfig',
        value: raw,
      });
      if (res.data.success) {
        showSuccess(t('保存成功'));
        setDraft(null);
        refresh?.();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('保存失败'));
    } finally {
      setLoading(false);
    }
  };

  // 干跑校验:把**编辑器里这份尚未保存的**配置发过去校验,让问题在点保存之前就暴露。
  const dryRun = async () => {
    let parsed;
    try {
      parsed = raw.trim() ? JSON.parse(raw) : [];
    } catch (e) {
      showError(t('配置不是合法 JSON：') + e.message);
      return;
    }
    try {
      setChecking(true);
      const res = await API.post('/api/option/aggregate_model_dry_run', {
        models: parsed,
      });
      if (res.data.success) {
        setResults(res.data.data);
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('校验失败'));
    } finally {
      setChecking(false);
    }
  };

  const levelTag = (level) => {
    if (level === 'error') return <Tag color='red'>{t('错误')}</Tag>;
    if (level === 'warn') return <Tag color='orange'>{t('注意')}</Tag>;
    return <Tag color='green'>{t('通过')}</Tag>;
  };

  return (
    <Card>
      <Form.Section text={t('聚合模型（流水线编排）')}>
        <Banner
          type='info'
          closeIcon={null}
          description={
            <div>
              {t(
                '一个对外模型名对应一条固定流水线：图片为「提示词增强 → 生成」，视频为「提示词增强 → 生成 → 超分」。',
              )}
              <br />
              {t(
                '聚合模型不会出现在模型广场与 /v1/models，只有知道模型名才能调用；分组与令牌白名单照常生效。',
              )}
              <br />
              {t(
                '各段独立计费：客户账单上会出现生成段、超分段与增强模型各自的消费记录。',
              )}
            </div>
          }
          style={{ marginBottom: 12 }}
        />

        <Form.TextArea
          field='AggregateModelConfig'
          label={t('配置（JSON）')}
          initValue={raw}
          value={raw}
          onChange={(v) => setDraft(v)}
          autosize={{ minRows: 12, maxRows: 30 }}
          placeholder={`[
  {
    "name": "h3-2k",
    "type": "video",
    "enabled": true,
    "generate": { "model": "minimax-h3", "overrides": { "size": "1280x720" } },
    "upscale":  { "model": "seedvr2-3b", "target_size": "2k" },
    "prompt_enhance": {
      "model": "gpt-4o-mini",
      "system_prompt": "把以下提示词改写得更适合视频生成模型……"
    }
  }
]`}
          extraText={
            <div>
              <Text type='secondary'>
                {t(
                  'generate.overrides 是客户参数的覆盖值：客户传的尺寸是他要的「最终」尺寸，生成段收到的应是「中间」尺寸，最终分辨率由超分段产出。',
                )}
              </Text>
              <br />
              <Text type='secondary'>
                {t(
                  'prompt_enhance 会把本次请求的输入图一并发给增强模型，因此请配置一个支持视觉的模型；纯文本模型可能直接忽略图片并照常返回文字，增强会静默退化成凭空臆造。',
                )}
              </Text>
            </div>
          }
        />

        <Space style={{ marginTop: 12 }}>
          <Button onClick={dryRun} loading={checking}>
            {t('干跑校验')}
          </Button>
          <Button theme='solid' onClick={submit} loading={loading}>
            {t('保存')}
          </Button>
        </Space>
        <div style={{ marginTop: 6 }}>
          <Text type='secondary' size='small'>
            {t(
              '干跑校验不发起任何真实调用，不消耗算力也不计费。它检查的是配置本身：模型能否路由、分组继承结果、超分模型是否具备超分能力等——这些体验区验证不了，且配错时不会报错，只会默默出差档。效果好不好请到体验区验证。',
            )}
          </Text>
        </div>

        {results && (
          <Card
            style={{ marginTop: 12 }}
            title={
              <Space>
                {t('校验结果')}
                {results.passed ? (
                  <Tag color='green'>{t('通过')}</Tag>
                ) : (
                  <Tag color='red'>{t('存在错误')}</Tag>
                )}
              </Space>
            }
          >
            {results.message && (
              <Banner
                type={results.passed ? 'info' : 'danger'}
                closeIcon={null}
                description={results.message}
                style={{ marginBottom: 12 }}
              />
            )}
            {(results.results || []).map((r) => (
              <div key={r.name} style={{ marginBottom: 16 }}>
                <Space>
                  <Text strong>{r.name}</Text>
                  {r.groups_inherited && (
                    <Tag color='blue'>
                      {t('分组继承自生成段')}
                      {r.groups?.length ? `：${r.groups.join(', ')}` : ''}
                    </Tag>
                  )}
                  {r.billable_models?.length > 0 && (
                    <Tag color='grey'>
                      {t('计费段')}：{r.billable_models.join(' + ')}
                    </Tag>
                  )}
                </Space>
                <div style={{ marginTop: 6 }}>
                  {(r.checks || []).map((ch, idx) => (
                    <div key={idx} style={{ marginBottom: 4 }}>
                      <Space align='start'>
                        {levelTag(ch.level)}
                        <Text
                          type={ch.level === 'error' ? 'danger' : undefined}
                        >
                          {ch.message}
                        </Text>
                      </Space>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </Card>
        )}
      </Form.Section>
    </Card>
  );
};

export default SettingsAggregateModel;
