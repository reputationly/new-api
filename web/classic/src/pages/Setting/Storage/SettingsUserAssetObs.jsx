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

// 系统设置 → 用户素材(OBS)。字段与后端 setting/system_setting/user_asset_storage.go
// 的 json tag 一一对应，key 前缀 user_asset_storage.。与媒体存储(OBS)相互独立：
// 媒体存储承接生图/生视频结果落盘，本配置承接画布素材库中用户主动上传的素材，
// 可绑定不同的桶与桶规则（生命周期/配额等）。未启用时素材库回落到媒体存储桶。
// AK/SK 经 GET 过滤不回显，表单留空表示「保持不变」；启用总开关时后端会跑一次连通性校验。
const ENABLED_KEY = 'user_asset_storage.enabled';
const SECRET_KEYS = [
  'user_asset_storage.access_key_id',
  'user_asset_storage.secret_access_key',
];

export default function SettingsUserAssetObs(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'user_asset_storage.enabled': false,
    'user_asset_storage.endpoint': '',
    'user_asset_storage.region': '',
    'user_asset_storage.bucket': '',
    'user_asset_storage.access_key_id': '',
    'user_asset_storage.secret_access_key': '',
    'user_asset_storage.signed_url_ttl_hours': 168,
    'user_asset_storage.max_object_size_mb': 200,
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
        // 未修改的 AK/SK 保持为空字符串，compareObjects 不会纳入，无需担心误清空。
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
        <Form.Section text={t('用户素材（OBS）')}>
          <Banner
            type='info'
            description={t(
              '画布素材库（用户主动上传的图片/视频/音频）的独立存储桶，与上方媒体存储互相独立，可配置不同的桶规则（生命周期、配额等）。启用后新上传的素材落此桶；存量素材保留在媒体存储桶，仍可正常访问，不做迁移。未启用时素材上传回落到媒体存储桶。启用时后端会用当前已保存的 Endpoint / Bucket / AK/SK 跑一次连通性校验（PutObject + DeleteObject），失败则拒绝启用。请先填好下面各项并保存，再打开此开关。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.Switch
                field={ENABLED_KEY}
                label={t('启用用户素材存储')}
                extraText={t('总开关；关闭时素材上传回落到媒体存储桶')}
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
                field={'user_asset_storage.bucket'}
                label={t('桶名 Bucket')}
                placeholder={'prod-newapi-user-assets-cn-central-221'}
                onChange={handleFieldChange('user_asset_storage.bucket')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field={'user_asset_storage.endpoint'}
                label={t('Endpoint')}
                placeholder={'https://obs.cn-central-221.ovaijisuan.com'}
                onChange={handleFieldChange('user_asset_storage.endpoint')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field={'user_asset_storage.region'}
                label={t('Region')}
                placeholder={'cn-central-221'}
                onChange={handleFieldChange('user_asset_storage.region')}
                showClear
              />
            </Col>
          </Row>
          <Banner
            type='warning'
            description={t(
              'AK/SK 加密后入库，保存后不回显。留空表示保持现有值不变；也可改用环境变量 USER_ASSET_OBS_AK / USER_ASSET_OBS_SK（优先级更高，且不入库）。',
            )}
            style={{ marginBottom: 16 }}
          />
          <Row gutter={16}>
            <Col xs={24} sm={12} md={12}>
              <Form.Input
                field={'user_asset_storage.access_key_id'}
                label={t('AccessKeyID')}
                mode='password'
                placeholder={t('留空表示不修改')}
                onChange={handleFieldChange('user_asset_storage.access_key_id')}
                showClear
              />
            </Col>
            <Col xs={24} sm={12} md={12}>
              <Form.Input
                field={'user_asset_storage.secret_access_key'}
                label={t('SecretAccessKey')}
                mode='password'
                placeholder={t('留空表示不修改')}
                onChange={handleFieldChange(
                  'user_asset_storage.secret_access_key',
                )}
                showClear
              />
            </Col>
          </Row>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field={'user_asset_storage.signed_url_ttl_hours'}
                label={t('签名 URL 有效期 (小时)')}
                extraText={t('默认 168 (7 天)，不应超过桶生命周期')}
                min={1}
                onChange={handleFieldChange(
                  'user_asset_storage.signed_url_ttl_hours',
                )}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.InputNumber
                field={'user_asset_storage.max_object_size_mb'}
                label={t('单素材上限 (MB)')}
                extraText={t('超过直接拒绝上传；用户容量配额另见画布配置')}
                min={1}
                onChange={handleFieldChange(
                  'user_asset_storage.max_object_size_mb',
                )}
              />
            </Col>
          </Row>
        </Form.Section>

        <Row>
          <Button size='default' onClick={onSubmit}>
            {t('保存用户素材存储设置')}
          </Button>
        </Row>
      </Form>
    </Spin>
  );
}
