import React, { useEffect, useState, useRef } from 'react';
import { Banner, Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

// 系统设置 → 审核取证（OBS）。字段与后端 setting/system_setting/moderation_storage.go
// 的 json tag 一一对应，key 前缀 moderation_storage.。
//
// 与媒体存储、用户素材三者互相独立。单独一个桶不是洁癖，是两条硬理由：
//   1. 主媒体桶挂着整桶 7 天的生命周期规则，而审核记录要留 180~360 天——同桶时
//      取证材料会在第 8 天被静默删掉，记录还在、图没了；
//   2. 这个桶里装的是违规内容，理应用一套只有管理端会用到的凭证。
//
// AK/SK 经 GET 过滤不回显（按 access_key_id / secret_access_key 后缀），
// 表单留空表示「保持不变」；启用总开关时后端会跑一次连通性校验。
const ENABLED_KEY = 'moderation_storage.enabled';
const SECRET_KEYS = [
  'moderation_storage.access_key_id',
  'moderation_storage.secret_access_key',
];

export default function SettingsModerationObs(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'moderation_storage.enabled': false,
    'moderation_storage.endpoint': '',
    'moderation_storage.region': '',
    'moderation_storage.bucket': '',
    'moderation_storage.access_key_id': '',
    'moderation_storage.secret_access_key': '',
    'moderation_storage.signed_url_ttl_hours': 1,
    'moderation_storage.max_object_size_mb': 200,
  });
  const [inputsRow, setInputsRow] = useState(inputs);
  const refForm = useRef();

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((prev) => ({ ...prev, [fieldName]: value }));
    };
  }

  async function putOption(key, value) {
    const res = await API.put('/api/option/', { key, value: String(value) });
    return res;
  }

  async function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));

    // 启用开关放到最后应用：后端在此时用「已保存的完整配置」跑连通性校验。
    const others = updateArray.filter((i) => i.key !== ENABLED_KEY);
    const enabledItem = updateArray.find((i) => i.key === ENABLED_KEY);

    setLoading(true);
    try {
      for (const item of others) {
        const res = await putOption(item.key, inputs[item.key]);
        if (!res?.data?.success) {
          showError(res?.data?.message || t('保存失败，请重试'));
          props.refresh();
          return;
        }
      }
      if (enabledItem) {
        const res = await putOption(ENABLED_KEY, inputs[ENABLED_KEY]);
        if (!res?.data?.success) {
          // 典型场景：启用时 OBS 连通性校验失败，后端返回具体原因。
          showError(res?.data?.message || t('启用失败'));
          props.refresh();
          return;
        }
      }
      showSuccess(t('保存成功'));
      props.refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
      props.refresh();
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        if (typeof inputs[key] === 'boolean') {
          currentInputs[key] =
            props.options[key] === 'true' || props.options[key] === true;
        } else if (typeof inputs[key] === 'number') {
          const n = parseFloat(props.options[key]);
          currentInputs[key] = isNaN(n) ? inputs[key] : n;
        } else {
          currentInputs[key] = props.options[key];
        }
      }
    }
    // AK/SK 后端不回显，始终以空串加载（留空=不修改）。
    for (const k of SECRET_KEYS) currentInputs[k] = '';
    const merged = { ...inputs, ...currentInputs };
    setInputs(merged);
    setInputsRow(merged);
    if (refForm.current) {
      refForm.current.setValues(merged);
    }
  }, [props.options]);

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('审核取证（OBS）')}>
          <Banner
            type='info'
            description={t(
              '被内容审核判定为违规的图片/视频会留存一份到此桶，供管理员在「拦截记录」页复核（判断是否误杀）。只留存判定为拦截的媒体，通过与待复核的不留存。启用时后端会用当前已保存的 Endpoint / Bucket / AK/SK 跑一次连通性校验，失败则拒绝启用——请先填好下面各项并保存，再打开此开关。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Banner
            type='warning'
            description={t(
              '强烈建议启用并使用独立的桶。未启用时取证材料回落到媒体存储桶的 moderation/ 前缀，而该桶通常挂着整桶 7 天的生命周期规则——审核记录留 180~360 天，图却会在第 8 天被静默删掉，等到要复核时才发现证据已经没了。此外这个桶里装的是违规内容，用独立凭证便于单独收紧权限。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.Switch
                field={ENABLED_KEY}
                label={t('启用审核取证存储')}
                extraText={t('关闭时回落到媒体存储桶的 moderation/ 前缀')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={handleFieldChange(ENABLED_KEY)}
              />
            </Col>
          </Row>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field={'moderation_storage.bucket'}
                label={t('桶名 Bucket')}
                placeholder={'prod-newapi-moderation-cn-central-221'}
                onChange={handleFieldChange('moderation_storage.bucket')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field={'moderation_storage.endpoint'}
                label={t('Endpoint')}
                placeholder={'https://obs.cn-central-221.ovaijisuan.com'}
                onChange={handleFieldChange('moderation_storage.endpoint')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field={'moderation_storage.region'}
                label={t('Region')}
                placeholder={'cn-central-221'}
                onChange={handleFieldChange('moderation_storage.region')}
                showClear
              />
            </Col>
          </Row>
          <Banner
            type='warning'
            description={t(
              'AK/SK 加密后入库，保存后不回显。留空表示保持现有值不变；也可改用环境变量 MODERATION_OBS_AK / MODERATION_OBS_SK（优先级更高，且不入库）。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Row gutter={16}>
            <Col xs={24} sm={12} md={12}>
              <Form.Input
                field={'moderation_storage.access_key_id'}
                label={t('AccessKeyID')}
                mode='password'
                placeholder={t('留空表示不修改')}
                onChange={handleFieldChange('moderation_storage.access_key_id')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={12}>
              <Form.Input
                field={'moderation_storage.secret_access_key'}
                label={t('SecretAccessKey')}
                mode='password'
                placeholder={t('留空表示不修改')}
                onChange={handleFieldChange(
                  'moderation_storage.secret_access_key',
                )}
                showClear
              />
            </Col>
          </Row>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field={'moderation_storage.signed_url_ttl_hours'}
                label={t('查看链接有效期 (小时)')}
                extraText={t(
                  '默认 1 小时，刻意远短于其它桶：它指向违规内容，只在管理员点开的那一刻有用',
                )}
                min={1}
                onChange={handleFieldChange(
                  'moderation_storage.signed_url_ttl_hours',
                )}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field={'moderation_storage.max_object_size_mb'}
                label={t('单个取证对象上限 (MB)')}
                extraText={t('超过则不留存；判定结果不受影响')}
                min={1}
                onChange={handleFieldChange(
                  'moderation_storage.max_object_size_mb',
                )}
              />
            </Col>
          </Row>
          <Banner
            type='info'
            description={t(
              '桶的生命周期规则请配成与「内容审核 → 拦截记录保留天数」一致。过期对象由后台任务按记录保留期主动删除，桶规则只作兜底——两边天数对不上时，要么记录还在图没了，要么图留着却再也没人能通过记录找到它。',
            )}
            style={{ marginTop: 8 }}
          />
        </Form.Section>

        <Row>
          <Button size='default' onClick={onSubmit}>
            {t('保存审核取证存储设置')}
          </Button>
        </Row>
      </Form>
    </Spin>
  );
}
