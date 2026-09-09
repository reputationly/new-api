import React, { useEffect, useRef, useState } from 'react';
import { Banner, Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import {
  API,
  compareObjects,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

// 内容审核设置。见 docs/content-moderation-design.md §8。
//
// 紧挨着「屏蔽词过滤设置」放：关键词层（L0）同时受两边开关约束
// （service/moderation/moderation.go:90），分开放会让人以为它们互不相干，
// 于是出现「审核已开但一条记录都没有」这种查不出原因的状态。
//
// 未纳入本卡片：policies / group_policies / endpoints。它们服务于 L1 及以上的
// 远程分类器，第一期没有任何消费方（activeModerators 只装配 L0），
// 做出来就是三块点不亮的配置。

/** model_filter 是嵌套结构，配置管理器把它整体存成一个 JSON 字符串键。 */
function parseModelFilter(raw) {
  if (!raw) return { mode: 'all', models: [] };
  try {
    const v = JSON.parse(raw);
    return { mode: v.mode || 'all', models: v.models || [] };
  } catch (e) {
    // 解析不出来按「全部」处理：这个字段决定哪些模型走审核，
    // 坏值当成 include 会静默漏审，当成 all 最多是多审几个模型。
    return { mode: 'all', models: [] };
  }
}

function buildModelFilter(mode, modelsText) {
  return JSON.stringify({
    mode,
    models: (modelsText || '')
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean),
  });
}

export default function SettingsModeration(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'moderation.mode': 'off',
    // 与 setting/system_setting/moderation.go 的 moderationSettings 保持一致，
    // 理由见 OperationSetting.jsx 同名键上的注释。
    'moderation.keyword_enabled': true,
    'moderation.model_filter': '',
    'moderation.log_pass_sample_rate': 0.01,
    'moderation.log_queue_size': 2048,
    'moderation.retention_block_days': 180,
    'moderation.retention_pass_days': 3,
    // 下面两个是纯 UI 字段，用来把 model_filter 那个 JSON 拆成两个控件。
    // 必须放进 inputs：Form 的 values 是受控的，不在 values 里的字段每次
    // 重渲染都会被置空，光靠 initValue 撑不住。提交时按前缀过滤掉。
    filterMode: 'all',
    filterModels: '',
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);
  const [status, setStatus] = useState(null);

  // 运行态。原文能不能留存取决于一个只在环境变量里的密钥，不在这里显示的话，
  // 「为什么看不到原文」只能靠翻服务日志回答（§8.4）。
  useEffect(() => {
    API.get('/api/moderation/status')
      .then((res) => {
        if (res.data?.success) setStatus(res.data.data);
      })
      .catch(() => {
        // 运行态拿不到不影响改配置，静默即可
      });
  }, []);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((prev) => ({ ...prev, [fieldName]: value }));
    };
  }

  // 两个 UI 控件同时写回 model_filter 那个 JSON 键，这样差异检测不用开特例。
  function handleFilterChange(nextMode, nextModelsText) {
    setInputs((prev) => {
      const mode = nextMode ?? prev.filterMode ?? 'all';
      const modelsText = nextModelsText ?? prev.filterModels ?? '';
      return {
        ...prev,
        filterMode: mode,
        filterModels: modelsText,
        'moderation.model_filter': buildModelFilter(mode, modelsText),
      };
    });
  }

  function onSubmit() {
    // 只提交真正的配置键：filterMode / filterModels 是本地拆出来的显示字段，
    // 提交上去会在 options 表里留下两个没人读的脏键。
    const updateArray = compareObjects(inputs, inputsRow).filter((item) =>
      item.key.startsWith('moderation.'),
    );
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) =>
      API.put('/api/option/', {
        key: item.key,
        value: String(inputs[item.key]),
      }),
    );
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (requestQueue.length === 1) {
          if (res.includes(undefined)) return;
        } else if (requestQueue.length > 1) {
          if (res.includes(undefined))
            return showError(t('部分保存失败，请重试'));
        }
        showSuccess(t('保存成功'));
        props.refresh();
      })
      .catch(() => {
        showError(t('保存失败，请重试'));
      })
      .finally(() => {
        setLoading(false);
      });
  }

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        currentInputs[key] = props.options[key];
      }
    }
    const filter = parseModelFilter(currentInputs['moderation.model_filter']);
    currentInputs.filterMode = filter.mode;
    currentInputs.filterModels = (filter.models || []).join('\n');
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);

  const mode = inputs['moderation.mode'];

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('内容审核')}>
            <Banner
              type='info'
              description={t(
                '对用户输入的提示词做审核。命中即在预扣费之前拒绝，不产生扣费；每次判定都会落一条审核记录，可在「审核记录」页查询。',
              )}
              style={{ marginBottom: 16 }}
            />
            {status?.encrypt_key_misconfigured && (
              <Banner
                type='danger'
                description={t(
                  'MODERATION_ENCRYPT_KEY 格式非法（需 64 位十六进制），被拦内容不会加密留存，事后无法复核原文。',
                )}
                style={{ marginBottom: 16 }}
              />
            )}
            {status &&
              !status.encrypt_key_ready &&
              !status.encrypt_key_misconfigured && (
                <Banner
                  type='warning'
                  description={t(
                    '未配置 MODERATION_ENCRYPT_KEY，审核照常运行，但被拦内容不会留存，事后无法复核原文。',
                  )}
                  style={{ marginBottom: 16 }}
                />
              )}
            {status?.dropped_logs > 0 && (
              <Banner
                type='warning'
                description={
                  t('审核日志队列已溢出，累计丢弃记录数：') +
                  status.dropped_logs +
                  t('。请调大下方的落库队列长度，否则记录里的空洞无法解释。')
                }
                style={{ marginBottom: 16 }}
              />
            )}

            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Select
                  field={'moderation.mode'}
                  label={t('运行模式')}
                  extraText={t(
                    '关闭：仅保留既有的屏蔽词拦截行为。仅观察：照常判定并记录，但不拒绝请求。拦截：判定为违规即拒绝。',
                  )}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange('moderation.mode')}
                >
                  <Form.Select.Option value='off'>
                    {t('关闭')}
                  </Form.Select.Option>
                  <Form.Select.Option value='observe'>
                    {t('仅观察')}
                  </Form.Select.Option>
                  <Form.Select.Option value='blocking'>
                    {t('拦截')}
                  </Form.Select.Option>
                </Form.Select>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'moderation.keyword_enabled'}
                  label={t('启用关键词层（L0）')}
                  extraText={t(
                    '还需上方「屏蔽词过滤设置」的两个开关同时开启，任一关闭即不生效。',
                  )}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('moderation.keyword_enabled')}
                />
              </Col>
            </Row>

            {mode === 'observe' && (
              <Banner
                type='warning'
                description={t(
                  '仅观察模式下关键词命中也只记录、不拦截，且只留 160 字符预览而非完整原文。它用来量误杀率，不是「软拦截」。',
                )}
                style={{ marginBottom: 16 }}
              />
            )}

            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Select
                  field={'filterMode'}
                  label={t('生效模型范围')}
                  extraText={t(
                    '范围外的模型退回「只跑关键词」，不是完全不审。',
                  )}
                  style={{ width: '100%' }}
                  onChange={(v) => handleFilterChange(v, null)}
                >
                  <Form.Select.Option value='all'>
                    {t('全部模型')}
                  </Form.Select.Option>
                  <Form.Select.Option value='include'>
                    {t('仅以下模型')}
                  </Form.Select.Option>
                  <Form.Select.Option value='exclude'>
                    {t('排除以下模型')}
                  </Form.Select.Option>
                </Form.Select>
              </Col>
              {inputs.filterMode !== 'all' && (
                <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                  <Form.TextArea
                    field={'filterModels'}
                    label={t('模型列表')}
                    extraText={t(
                      '一行一个，只支持结尾 * 通配（如 text-embedding-*）；不支持正则——正则写错不报错，只会静默漏审。',
                    )}
                    style={{ fontFamily: 'JetBrains Mono, Consolas' }}
                    autosize={{ minRows: 3, maxRows: 8 }}
                    onChange={(v) => handleFilterChange(null, v)}
                  />
                </Col>
              )}
            </Row>

            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'moderation.log_pass_sample_rate'}
                  label={t('放行记录抽样率')}
                  extraText={t(
                    '0~1。拦截记录恒全量落库；关闭模式下不写放行记录。',
                  )}
                  min={0}
                  max={1}
                  step={0.01}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange(
                    'moderation.log_pass_sample_rate',
                  )}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'moderation.log_queue_size'}
                  label={t('落库队列长度')}
                  extraText={t(
                    '异步落库，队列满时丢日志而不是阻塞请求。重启后生效。',
                  )}
                  min={1}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange('moderation.log_queue_size')}
                />
              </Col>
            </Row>

            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'moderation.retention_block_days'}
                  label={t('拦截记录保留天数')}
                  extraText={t(
                    '清理是物理删除，没有第二份可恢复，只宜调大不宜调小。',
                  )}
                  min={1}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange(
                    'moderation.retention_block_days',
                  )}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'moderation.retention_pass_days'}
                  label={t('放行记录保留天数')}
                  extraText={t('放行记录不含内容，只有 hash 与元数据。')}
                  min={1}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange('moderation.retention_pass_days')}
                />
              </Col>
            </Row>

            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存内容审核设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
