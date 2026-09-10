import React, { useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
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
// 未纳入本卡片：policies / group_policies。它们目前只有一条内置的「标准」策略，
// 且没有分组灰度的消费方；等要按分组放量时再补。

/** 新增节点的默认值。取值依据见各字段的 extraText。 */
const newEndpoint = () => ({
  name: '',
  base_url: '',
  model: '',
  api_key: '',
  modality: 'text',
  timeout_ms: 3000,
  input_limit: 24000,
  enabled: true,
});

/**
 * 保存前校验。与后端 validateModerationEndpoints 一致：
 * 名称是这套配置事实上的主键（凭证按名回捞、故障冻结按名记），
 * 启用却填不全的节点会进调用轮换，在拦截模式下把每次审核都变成失败。
 */
function validateEndpoints(endpoints) {
  const seen = new Set();
  for (let i = 0; i < endpoints.length; i++) {
    const e = endpoints[i];
    const name = (e.name || '').trim();
    if (!name) return `第 ${i + 1} 个审核节点没有填名称`;
    if (seen.has(name)) return `审核节点名称重复：${name}`;
    seen.add(name);
    // 改名会让密钥回捞落空：后端按 name 去旧配置里取原密文（api_key 留空表示
    // 「保持不变」），名字一改就找不到，凭证被静默清空，之后每次调用 401、
    // 节点被冻结 10 分钟，拦截模式下就是全站拒绝——而界面只会显示「保存成功」。
    if (
      e.has_api_key &&
      e._originalName &&
      name !== e._originalName &&
      !e.api_key
    ) {
      return `审核节点「${e._originalName}」改名为「${name}」后，需要重新填写 API Key（密钥是按名称保管的，改名后找不回原值）`;
    }
    if (!e.enabled) continue;
    if (!(e.base_url || '').trim() || !(e.model || '').trim()) {
      return `审核节点 ${name} 已启用，但地址或模型名为空`;
    }
  }
  return '';
}

function parseEndpoints(raw) {
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v : [];
  } catch (e) {
    return [];
  }
}

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
    // 产物侧（我们生成的图片/视频）。与 mode 分开：产物违规是模型的问题，
    // 用户的 prompt 可能完全无辜，两侧的处置口径不同（§12.4.5）。
    'moderation.output_mode': 'off',
    // 与 setting/system_setting/moderation.go 的 moderationSettings 保持一致，
    // 理由见 OperationSetting.jsx 同名键上的注释。
    'moderation.keyword_enabled': true,
    'moderation.fail_open': true,
    'moderation.model_filter': '',
    // 声明它只是为了让 props.options 里的值能落进 currentInputs；实际编辑走独立的
    // endpoints state，提交时在 onSubmit 里单独处理。
    'moderation.endpoints': '',
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

  // 审核节点是动态列表，不走 Form 的受控 values（那套按固定字段名绑定，
  // 增删行会很别扭），单独用一份 state，提交时序列化成 moderation.endpoints。
  const [endpoints, setEndpoints] = useState([]);
  const [endpointsRow, setEndpointsRow] = useState('[]');
  // 每个节点的测试结果，按下标存。key 用下标而不是 name：名字本身可编辑，
  // 改到一半时用它当 key 会让结果错位到别的行上。
  const [testResults, setTestResults] = useState({});

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

  function updateEndpoint(idx, field, value) {
    setEndpoints((prev) =>
      prev.map((e, i) => {
        if (i !== idx) return e;
        const next = { ...e, [field]: value };
        // 切成图片节点时把超时抬上来：视觉推理比文本慢一个量级，
        // 文本的 3000ms 默认值会让图片判定频繁超时，而超时在拦截模式下
        // 是 fail-close，表现为「正常请求被拒」而不是「审核慢」。
        if (
          field === 'modality' &&
          value === 'image' &&
          next.timeout_ms < 10000
        ) {
          next.timeout_ms = 10000;
        }
        return next;
      }),
    );
    // 改了任何字段，之前那条测试结论就不再代表当前配置，必须清掉——
    // 留着会让人看着一个绿标去保存一份改坏了的配置。
    setTestResults((prev) => ({ ...prev, [idx]: undefined }));
  }

  async function testEndpoint(idx) {
    const ep = endpoints[idx];
    if (!ep.base_url || !ep.model) {
      showWarning(t('请先填写地址与模型名'));
      return;
    }
    setTestResults((prev) => ({ ...prev, [idx]: { loading: true } }));
    try {
      const res = await API.post('/api/moderation/test-endpoint', {
        name: ep.name,
        base_url: ep.base_url,
        model: ep.model,
        api_key: ep.api_key,
        // 必须带上模态：图片节点要用一张真实的图去测，请求形状与文本完全不同。
        // 不传的话后端按文本测，而视觉模型照样能回答纯文本对话——按钮报绿，
        // 但生产真正会走的那条请求形状一次都没验证过。
        modality: ep.modality || 'text',
        timeout_ms: ep.timeout_ms,
      });
      const { success, message, data } = res.data;
      setTestResults((prev) => ({
        ...prev,
        [idx]: success
          ? {
              ok: true,
              latency: data?.latency_ms,
              raw: data?.raw,
              parsedOk: data?.parsed_ok,
            }
          : { ok: false, msg: message },
      }));
    } catch (e) {
      setTestResults((prev) => ({
        ...prev,
        [idx]: { ok: false, msg: t('请求失败') },
      }));
    }
  }

  function onSubmit() {
    // 只提交真正的配置键：filterMode / filterModels 是本地拆出来的显示字段，
    // 提交上去会在 options 表里留下两个没人读的脏键。endpoints 走独立 state，
    // 这里也要排除掉，否则会用未更新的旧值把刚编辑的内容覆盖回去。
    const updateArray = compareObjects(inputs, inputsRow).filter(
      (item) =>
        item.key.startsWith('moderation.') &&
        item.key !== 'moderation.endpoints',
    );
    // 后端 validateModerationEndpoints 会硬拦这几条，这里先做一次即时反馈，
    // 免得点了保存才在 toast 里看到错误、还要自己数是第几个节点。
    const invalid = validateEndpoints(endpoints);
    if (invalid) return showError(invalid);

    // _originalName 是纯本地的比对基准，不能写进配置。
    const nextEndpoints = JSON.stringify(
      endpoints.map(({ _originalName, ...rest }) => rest),
    );
    const endpointsChanged = nextEndpoints !== endpointsRow;
    if (!updateArray.length && !endpointsChanged) {
      return showWarning(t('你似乎并没有修改什么'));
    }
    const requestQueue = updateArray.map((item) =>
      API.put('/api/option/', {
        key: item.key,
        value: String(inputs[item.key]),
      }),
    );
    if (endpointsChanged) {
      requestQueue.push(
        API.put('/api/option/', {
          key: 'moderation.endpoints',
          value: nextEndpoints,
        }),
      );
    }
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (res.includes(undefined)) {
          return showError(t('部分保存失败，请重试'));
        }
        // 后端拒绝走的是 HTTP 200 + {success:false}（common.ApiError），axios 拦截器
        // 只管传输层错误和 401，所以这里必须自己看响应体。不看的话，节点保存被后端
        // 拒掉（最常见的是没配 MODERATION_ENCRYPT_KEY，Encrypt 会整体拒绝）也会显示
        // 「保存成功」，接着 refresh 把卡片悄悄刷回旧值——管理员以为配好了，实际
        // 一个凭证都没存下，拦截模式下就是 401 → 冻结 → 全站 fail-close。
        const failed = res.find((r) => r?.data?.success === false);
        if (failed) {
          return showError(failed.data.message || t('保存失败，请重试'));
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

    // 后端回显时会把每条的 api_key 抹成空（RedactModerationEndpoints），
    // 所以这里拿到的密钥一定是空的；提交时留空表示「保持不变」，由
    // EncryptModerationEndpoints 按 name 取回原密文。
    // 记住加载时的名字：保存时据此判断这一行是否被改名（见 validateEndpoints）。
    const eps = parseEndpoints(props.options['moderation.endpoints']).map(
      (e) => ({
        ...e,
        _originalName: e.name,
      }),
    );
    setEndpoints(eps);
    setEndpointsRow(
      JSON.stringify(eps.map(({ _originalName, ...rest }) => rest)),
    );
    setTestResults({});
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
                '对用户输入的提示词做审核。命中即在预扣费之前拒绝，不产生扣费；每次判定都会落一条记录，可在「拦截记录」页查询。',
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
            {status?.frozen_endpoints &&
              Object.keys(status.frozen_endpoints).length > 0 && (
                <Banner
                  type='danger'
                  description={
                    t('以下审核节点正处于冻结状态：') +
                    Object.keys(status.frozen_endpoints).join('、') +
                    t(
                      '。拦截模式下审核失败会拒绝请求——先确认是节点故障还是密钥失效，别把它当成用户在违规。',
                    )
                  }
                  style={{ marginBottom: 16 }}
                />
              )}
            {status?.fail_open_count > 0 && (
              <Banner
                type='warning'
                description={
                  t('因审核服务不可用而放行的请求数：') +
                  status.fail_open_count +
                  t('。这些请求没有经过审核，不是审核通过。')
                }
                style={{ marginBottom: 16 }}
              />
            )}
            {status?.fail_close_count > 0 && (
              <Banner
                type='warning'
                description={
                  t('因审核未能完成而被拒绝的请求数：') +
                  status.fail_close_count +
                  t('。这是审核服务的问题，不是用户违规。')
                }
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
            {/*
              视频审核依赖服务端的 ffmpeg。缺了不会拒绝请求（那会把部署问题变成事故），
              而是跳过视频——所以必须在这里说出来：否则「视频都审过了」和「视频一个都
              没审」在这个页面上长得一模一样，而后者会一直持续到有人来问为什么没拦住。
              只在配了图片节点时提示：没配图片节点时视频本来就不审，说 ffmpeg 是噪音。
            */}
            {status?.ffmpeg_ready === false &&
              endpoints.some((e) => e.modality === 'image' && e.enabled) && (
                <Banner
                  type='warning'
                  description={
                    t(
                      '服务端未安装 ffmpeg，上传的视频不会经过审核（图片审核不受影响）。',
                    ) +
                    (status.video_skipped_count > 0
                      ? t('已跳过视频数：') + status.video_skipped_count
                      : '')
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
                <Form.Select
                  field={'moderation.output_mode'}
                  label={t('产物运行模式')}
                  extraText={t(
                    '审我们生成的图片与视频（文本输出不审）。异步任务在完成后、用户取件前审，零额外延迟；判定违规则任务转失败，暂不退款（按实际消耗计费，用户可凭失败原因申诉）。',
                  )}
                  style={{ width: '100%' }}
                  onChange={handleFieldChange('moderation.output_mode')}
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
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'moderation.fail_open'}
                  label={t('审核服务不可用时放行')}
                  extraText={t(
                    '开启：审核节点挂掉或升级时请求照常放行（记录里仍标记为未审核）。关闭：拦截模式下会全站拒绝。',
                  )}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('moderation.fail_open')}
                />
              </Col>
            </Row>

            {['observe', 'blocking'].includes(
              inputs['moderation.output_mode'],
            ) &&
              !endpoints.some((e) => e.modality === 'image' && e.enabled) && (
                <Banner
                  type='danger'
                  description={t(
                    '产物审核已开启，但没有启用中的「图片 / 视频」审核节点——产物一个都不会被审。请在下方添加一个模态为「图片 / 视频」的节点。',
                  )}
                  style={{ marginBottom: 16 }}
                />
              )}
            {inputs['moderation.fail_open'] && mode === 'blocking' && (
              <Banner
                type='warning'
                description={t(
                  '已开启「服务不可用时放行」：审核节点挂掉或升级期间，请求不会被拒绝。文本侧仍有关键词层兜底，但图片/视频侧没有任何兜底（关键词扫不了图），这段时间上传的媒体完全不经审核。放行次数可在上方运行态查看。',
                )}
                style={{ marginBottom: 16 }}
              />
            )}
            {!inputs['moderation.fail_open'] && mode === 'blocking' && (
              <Banner
                type='danger'
                description={t(
                  '已关闭「服务不可用时放行」：审核节点全部不可用时，拦截模式下所有请求都会被拒绝（503）。计划内维护（如 GPUStack 升级）前请先停用下方的审核节点，而不是依赖这个开关。',
                )}
                style={{ marginBottom: 16 }}
              />
            )}

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

            {/* ── 审核节点（L1 模型层） ─────────────────────────────── */}
            <Typography.Title heading={6} style={{ marginTop: 24 }}>
              {t('审核节点（L1 模型层）')}
            </Typography.Title>
            <Typography.Text
              type='tertiary'
              style={{ display: 'block', marginBottom: 12 }}
            >
              {t(
                '远程分类器节点，运行模式为「关闭」时不会调用。留空则只跑关键词层。多节点会逐个轮换，失败按 HTTP 状态分级冻结。',
              )}
            </Typography.Text>

            {endpoints.length === 0 && (
              <Typography.Text
                type='tertiary'
                style={{ display: 'block', marginBottom: 12 }}
              >
                {t('尚未配置审核节点')}
              </Typography.Text>
            )}

            {endpoints.map((ep, idx) => {
              const r = testResults[idx];
              return (
                <Card
                  key={idx}
                  style={{ marginBottom: 12 }}
                  bodyStyle={{ padding: 16 }}
                >
                  <Row gutter={12}>
                    <Col xs={24} sm={12} md={6}>
                      <div style={{ marginBottom: 4 }}>{t('节点名称')}</div>
                      <Input
                        value={ep.name}
                        placeholder='guard-1'
                        onChange={(v) => updateEndpoint(idx, 'name', v)}
                      />
                    </Col>
                    <Col xs={24} sm={12} md={10}>
                      <div style={{ marginBottom: 4 }}>{t('地址')}</div>
                      <Input
                        value={ep.base_url}
                        placeholder='http://10.0.0.238'
                        onChange={(v) => updateEndpoint(idx, 'base_url', v)}
                      />
                    </Col>
                    <Col xs={24} sm={12} md={5}>
                      <div style={{ marginBottom: 4 }}>{t('模型名称')}</div>
                      <Input
                        value={ep.model}
                        placeholder={
                          ep.modality === 'image'
                            ? 'shieldgemma2'
                            : 'qwen3guard'
                        }
                        onChange={(v) => updateEndpoint(idx, 'model', v)}
                      />
                    </Col>
                    {/*
                      模态决定这个节点审什么，也决定判定请求长什么样。
                      没有这个选择器时 ImageEndpoints() 恒为空，整条图片/视频审核链
                      从界面上就无法启用——只能手改 options 表里的 JSON。
                    */}
                    <Col xs={24} sm={12} md={3}>
                      <div style={{ marginBottom: 4 }}>{t('模态')}</div>
                      <Select
                        value={ep.modality || 'text'}
                        style={{ width: '100%' }}
                        onChange={(v) => updateEndpoint(idx, 'modality', v)}
                      >
                        <Select.Option value='text'>{t('文本')}</Select.Option>
                        <Select.Option value='image'>
                          {t('图片 / 视频')}
                        </Select.Option>
                      </Select>
                    </Col>
                  </Row>

                  <Row gutter={12} style={{ marginTop: 12 }}>
                    <Col xs={24} sm={12} md={8}>
                      <div style={{ marginBottom: 4 }}>{t('API Key')}</div>
                      <Input
                        mode='password'
                        value={ep.api_key}
                        placeholder={t('留空表示不修改已保存的密钥')}
                        onChange={(v) => updateEndpoint(idx, 'api_key', v)}
                      />
                    </Col>
                    <Col xs={12} sm={6} md={4}>
                      <div style={{ marginBottom: 4 }}>{t('超时（毫秒）')}</div>
                      <InputNumber
                        value={ep.timeout_ms}
                        min={100}
                        style={{ width: '100%' }}
                        onChange={(v) => updateEndpoint(idx, 'timeout_ms', v)}
                      />
                    </Col>
                    <Col xs={12} sm={6} md={4}>
                      {/*
                        分段上限只对文本节点有意义（超长输入按它切段）。图片节点
                        没有「分段」这回事，留着一个改了也不起作用的输入框，
                        只会让人以为调它能影响图片审核。
                      */}
                      {ep.modality === 'image' ? (
                        <>
                          <div style={{ marginBottom: 4 }}>{t('分段上限')}</div>
                          <Input disabled value={t('图片节点不适用')} />
                        </>
                      ) : (
                        <>
                          <div style={{ marginBottom: 4 }}>{t('分段上限')}</div>
                          <InputNumber
                            value={ep.input_limit}
                            min={128}
                            style={{ width: '100%' }}
                            onChange={(v) =>
                              updateEndpoint(idx, 'input_limit', v)
                            }
                          />
                        </>
                      )}
                    </Col>
                    <Col xs={12} sm={6} md={3}>
                      <div style={{ marginBottom: 4 }}>{t('启用')}</div>
                      <Switch
                        checked={ep.enabled}
                        onChange={(v) => updateEndpoint(idx, 'enabled', v)}
                      />
                    </Col>
                    <Col xs={12} sm={6} md={5}>
                      <div style={{ marginBottom: 4 }}>&nbsp;</div>
                      <Space>
                        <Button
                          size='small'
                          loading={r?.loading}
                          onClick={() => testEndpoint(idx)}
                        >
                          {t('测试连接')}
                        </Button>
                        <Button
                          size='small'
                          type='danger'
                          onClick={() => {
                            setEndpoints((prev) =>
                              prev.filter((_, i) => i !== idx),
                            );
                            setTestResults({});
                          }}
                        >
                          {t('删除')}
                        </Button>
                      </Space>
                    </Col>
                  </Row>

                  {r && !r.loading && (
                    <div style={{ marginTop: 12 }}>
                      {r.ok ? (
                        <Space>
                          {/* parsed_ok 才是真通：返回 200 但输出不是 Safety/Categories，
                              说明部署的不是 guard 模型，审核链路会把每次判定都当异常，
                              拦截模式下就是全站拒绝。 */}
                          <Tag color={r.parsedOk ? 'green' : 'orange'}>
                            {r.parsedOk ? t('连接正常') : t('能连通但输出异常')}
                          </Tag>
                          <Typography.Text type='tertiary'>
                            {r.latency}ms · {r.raw}
                          </Typography.Text>
                        </Space>
                      ) : (
                        <Space>
                          <Tag color='red'>{t('连接失败')}</Tag>
                          <Typography.Text type='danger'>
                            {r.msg}
                          </Typography.Text>
                        </Space>
                      )}
                    </div>
                  )}
                </Card>
              );
            })}

            <Row style={{ marginBottom: 16 }}>
              <Button
                size='default'
                onClick={() => setEndpoints((prev) => [...prev, newEndpoint()])}
              >
                {t('添加审核节点')}
              </Button>
            </Row>

            <Row>
              <Button size='default' type='primary' onClick={onSubmit}>
                {t('保存内容审核设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
