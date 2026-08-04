import React, { useEffect, useRef, useState } from 'react';
import { Button, Col, Form, Row, Spin, Typography } from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

// 管理员通知:用户提交待审事项(工单/实名/企业认证/企业转账/开票)时,通过企业微信、
// 钉钉群机器人 webhook 提醒管理员。后端见 service/admin_notify.go —— 事件开关关闭或两个
// webhook 都为空时直接静默返回,所以这里留空即等于关掉该渠道。
const EVENT_FIELDS = [
  { key: 'notification_setting.notify_feedback', label: '新工单' },
  { key: 'notification_setting.notify_kyc', label: '实名认证提交' },
  { key: 'notification_setting.notify_enterprise', label: '企业认证提交' },
  { key: 'notification_setting.notify_bank_transfer', label: '企业转账提交' },
  { key: 'notification_setting.notify_invoice', label: '开票申请提交' },
];

const isValidWebhook = (url) => !url || /^https?:\/\/.+/.test(url.trim());

export default function SettingsAdminNotification(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  // 正在测试的渠道('wechat_work' / 'dingtalk'),用于两个按钮互斥禁用。
  const [testing, setTesting] = useState(null);
  const [inputs, setInputs] = useState({
    'notification_setting.wechat_work_webhook_url': '',
    'notification_setting.dingtalk_webhook_url': '',
    'notification_setting.notify_feedback': false,
    'notification_setting.notify_kyc': false,
    'notification_setting.notify_enterprise': false,
    'notification_setting.notify_bank_transfer': false,
    'notification_setting.notify_invoice': false,
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((inputs) => ({ ...inputs, [fieldName]: value }));
    };
  }

  function onSubmit() {
    const wechat = inputs['notification_setting.wechat_work_webhook_url'];
    const dingtalk = inputs['notification_setting.dingtalk_webhook_url'];
    if (!isValidWebhook(wechat) || !isValidWebhook(dingtalk)) {
      return showError(t('请填写正确的 Webhook 地址(以 http:// 或 https:// 开头)'));
    }

    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      const value = inputs[item.key];
      return API.put('/api/option/', {
        key: item.key,
        value: typeof value === 'string' ? value.trim() : String(value),
      });
    });
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

  // 发送测试:打的是当前已保存的配置,所以改了地址要先保存再测。
  async function onTest(channel) {
    setTesting(channel);
    try {
      const res = await API.post('/api/option/notification_test', { channel });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('测试通知已发送'));
      } else {
        showError(message || t('测试发送失败'));
      }
    } catch (error) {
      showError(error.message || t('测试发送失败'));
    } finally {
      setTesting(null);
    }
  }

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        currentInputs[key] = props.options[key];
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('管理员通知')}>
            <Typography.Text
              type='tertiary'
              style={{ marginBottom: 16, display: 'block' }}
            >
              {t(
                '用户提交待审核事项时，通过企业微信、钉钉群机器人提醒管理员。两个地址可同时配置，留空即关闭该渠道。',
              )}
            </Typography.Text>
            <Row gutter={16}>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field={'notification_setting.wechat_work_webhook_url'}
                  label={t('企业微信 Webhook 地址')}
                  placeholder='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
                  onChange={handleFieldChange(
                    'notification_setting.wechat_work_webhook_url',
                  )}
                  extraText={t('群机器人的 Webhook 地址')}
                />
                <Button
                  size='default'
                  style={{ marginTop: 8 }}
                  loading={testing === 'wechat_work'}
                  disabled={testing !== null}
                  onClick={() => onTest('wechat_work')}
                >
                  {t('发送测试')}
                </Button>
              </Col>
              <Col xs={24} sm={24} md={12} lg={12} xl={12}>
                <Form.Input
                  field={'notification_setting.dingtalk_webhook_url'}
                  label={t('钉钉 Webhook 地址')}
                  placeholder='https://oapi.dingtalk.com/robot/send?access_token=...'
                  onChange={handleFieldChange(
                    'notification_setting.dingtalk_webhook_url',
                  )}
                  // 后端所有通知文案都固定带「管理员通知」四个字(adminNotifyKeyword),
                  // 就是为了让钉钉的自定义关键词能放行;不说明用户会配不通。
                  extraText={t(
                    '群机器人的 Webhook 地址。需在钉钉侧把「管理员通知」加为自定义关键词，或将服务器 IP 加入白名单。',
                  )}
                />
                <Button
                  size='default'
                  style={{ marginTop: 8 }}
                  loading={testing === 'dingtalk'}
                  disabled={testing !== null}
                  onClick={() => onTest('dingtalk')}
                >
                  {t('发送测试')}
                </Button>
              </Col>
            </Row>
            <Typography.Text
              type='tertiary'
              style={{ margin: '16px 0 8px', display: 'block' }}
            >
              {t('通知事件')}
            </Typography.Text>
            <Row gutter={16}>
              {EVENT_FIELDS.map((item) => (
                <Col key={item.key} xs={24} sm={12} md={8} lg={8} xl={8}>
                  <Form.Switch
                    field={item.key}
                    label={t(item.label)}
                    size='default'
                    checkedText='｜'
                    uncheckedText='〇'
                    onChange={handleFieldChange(item.key)}
                  />
                </Col>
              ))}
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存通知设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
