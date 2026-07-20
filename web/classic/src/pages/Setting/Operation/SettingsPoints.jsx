import React, { useEffect, useState, useRef } from 'react';
import {
  Button,
  Col,
  Form,
  Row,
  Spin,
  Typography,
  Banner,
  TagInput,
} from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

export default function SettingsPoints(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'points_setting.enabled': false,
    'points_setting.require_kyc': true,
    'points_setting.quota_per_point': 684.93,
    'points_setting.enabled_groups': '[]', // 以 JSON 字符串存储，避免 compareObjects 误判
    'points_setting.kyc_verified_points': 0,
    'points_setting.kyc_inviter_points': 0,
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((inputs) => ({ ...inputs, [fieldName]: value }));
    };
  }

  // enabled_groups 在 inputs 里是 JSON 字符串，TagInput 需要数组，边界处转换
  function getGroupsArray() {
    try {
      return JSON.parse(inputs['points_setting.enabled_groups'] || '[]');
    } catch {
      return [];
    }
  }

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      const value = String(inputs[item.key]);
      return API.put('/api/option/', {
        key: item.key,
        value,
      });
    });
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (res.includes(undefined))
          return showError(t('部分保存失败，请重试'));
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
    setInputs((prev) => ({ ...prev, ...currentInputs }));
    setInputsRow(structuredClone({ ...inputs, ...currentInputs }));
    if (refForm.current) {
      refForm.current.setValues({ ...inputs, ...currentInputs });
    }
  }, [props.options]);

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('积分设置')}>
            <Banner
              type='info'
              description={t(
                '积分是独立于余额的营销赠送钱包，1 积分 ≈ 1 分钱（可调）。白名单分组下积分优先抵扣、不足扣余额；非白名单分组只扣余额。',
              )}
              style={{ marginBottom: 16 }}
            />
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'points_setting.enabled'}
                  label={t('启用积分系统')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('points_setting.enabled')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'points_setting.require_kyc'}
                  label={t('未实名用户不参加积分')}
                  extraText={t('发放与使用双卡：未实名不发、积分冻结，实名后解冻')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('points_setting.require_kyc')}
                  disabled={!inputs['points_setting.enabled']}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'points_setting.quota_per_point'}
                  label={t('每积分对应额度(quota unit)')}
                  extraText={t('1 积分 = 1 分钱时约为 684.93，上线后不建议修改')}
                  onChange={handleFieldChange('points_setting.quota_per_point')}
                  min={0}
                  step={0.001}
                  disabled={!inputs['points_setting.enabled']}
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'points_setting.kyc_verified_points'}
                  label={t('实名认证赠送积分(本人)')}
                  extraText={t('0 = 关闭')}
                  onChange={handleFieldChange(
                    'points_setting.kyc_verified_points',
                  )}
                  min={0}
                  disabled={!inputs['points_setting.enabled']}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  field={'points_setting.kyc_inviter_points'}
                  label={t('被邀请人实名赠送邀请人积分')}
                  extraText={t('0 = 关闭；邀请人本人须已实名')}
                  onChange={handleFieldChange(
                    'points_setting.kyc_inviter_points',
                  )}
                  min={0}
                  disabled={!inputs['points_setting.enabled']}
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={24} md={16} lg={16} xl={16}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text strong>
                    {t('允许积分抵扣的分组白名单')}
                  </Typography.Text>
                  <Typography.Text
                    type='tertiary'
                    style={{ marginLeft: 8, fontSize: 12 }}
                  >
                    {t('留空 = 所有分组只扣余额（采购分组零配置即安全）')}
                  </Typography.Text>
                </div>
                <TagInput
                  placeholder={t('输入分组名后回车，如 vip、self-hosted')}
                  value={getGroupsArray()}
                  onChange={(arr) =>
                    handleFieldChange('points_setting.enabled_groups')(
                      JSON.stringify(arr),
                    )
                  }
                  disabled={!inputs['points_setting.enabled']}
                  style={{ width: '100%' }}
                />
              </Col>
            </Row>
            <Row style={{ marginTop: 16 }}>
              <Button size='default' onClick={onSubmit}>
                {t('保存积分设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
