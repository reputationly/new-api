/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useContext, useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { useTranslation } from 'react-i18next';

export default function GeneralSettings(props) {
  const { t } = useTranslation();
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    TopUpLink: '',
    'general_setting.docs_link': '',
    'general_setting.assistant_link': '',
    'general_setting.default_chat_model': '',
    'general_setting.model_modalities': '',
    RetryTimes: '',
    DisplayTokenStatEnabled: false,
    DefaultCollapseSidebar: false,
    DemoSiteEnabled: false,
    SelfUseModeEnabled: false,
    'token_setting.max_user_tokens': 1000,
    'sub_account_setting.max_count': 10,
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((inputs) => ({ ...inputs, [fieldName]: value }));
    };
  }

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = inputs[item.key];
      }
      return API.put('/api/option/', {
        key: item.key,
        value,
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
        // 顶栏和站内「蒜狸小助手」页都读 StatusContext 里的 assistant_link，
        // 保存后立即同步，否则管理员要刷新页面才能看到新入口。
        if (statusState?.status) {
          statusDispatch({
            type: 'set',
            payload: {
              ...statusState.status,
              assistant_link: inputs['general_setting.assistant_link'] || '',
            },
          });
        }
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
          <Form.Section text={t('通用设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'TopUpLink'}
                  label={t('充值链接')}
                  initValue={''}
                  placeholder={t('例如发卡网站的购买链接')}
                  onChange={handleFieldChange('TopUpLink')}
                  showClear
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'general_setting.docs_link'}
                  label={t('文档地址')}
                  initValue={''}
                  placeholder={t('例如 https://docs.newapi.pro')}
                  onChange={handleFieldChange('general_setting.docs_link')}
                  showClear
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'general_setting.assistant_link'}
                  label={t('蒜狸小助手页面地址')}
                  initValue={''}
                  placeholder={t('例如 https://example.com/assistant')}
                  extraText={t(
                    '配置后作为站内「蒜狸小助手」页面嵌入显示（目标页需允许 iframe），下载链接由该页面决定；入口在「顶栏管理」控制',
                  )}
                  onChange={handleFieldChange('general_setting.assistant_link')}
                  showClear
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'general_setting.default_chat_model'}
                  label={t('默认对话模型')}
                  initValue={''}
                  placeholder={t('例如 deepseek-v4-1-flash-1017')}
                  extraText={t(
                    '能力目录会把该模型标为对话默认项；留空则沿用"按出现顺序第一条"的旧规则。客户端据此直接选中，不再各自硬编码',
                  )}
                  onChange={handleFieldChange('general_setting.default_chat_model')}
                  showClear
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.TextArea
                  field={'general_setting.model_modalities'}
                  label={t('模型输入模态')}
                  initValue={''}
                  autosize={{ minRows: 3, maxRows: 8 }}
                  placeholder={'{"qwen3.8-flash":["image","video"]}'}
                  extraText={t(
                    'JSON 对象：模型 → 支持的输入模态（image/video/audio）。留空使用出厂已知表；写入后覆盖同名条目',
                  )}
                  onChange={handleFieldChange('general_setting.model_modalities')}
                />
              </Col>
              {/* 单位美元额度已合入汇率组合控件（TOKENS 模式下编辑），不再单独展示 */}
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'RetryTimes'}
                  label={t('失败重试次数')}
                  initValue={''}
                  placeholder={t('失败重试次数')}
                  onChange={handleFieldChange('RetryTimes')}
                  showClear
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'DisplayTokenStatEnabled'}
                  label={t('额度查询接口返回令牌额度而非用户额度')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('DisplayTokenStatEnabled')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'DefaultCollapseSidebar'}
                  label={t('默认折叠侧边栏')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('DefaultCollapseSidebar')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'DemoSiteEnabled'}
                  label={t('演示站点模式')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('DemoSiteEnabled')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'SelfUseModeEnabled'}
                  label={t('自用模式')}
                  extraText={t('开启后不限制：必须设置模型倍率')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={handleFieldChange('SelfUseModeEnabled')}
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('用户最大令牌数量')}
                  field={'token_setting.max_user_tokens'}
                  step={1}
                  min={1}
                  extraText={t(
                    '每个用户最多可创建的令牌数量，默认 1000，设置过大可能会影响性能',
                  )}
                  placeholder={'1000'}
                  onChange={handleFieldChange('token_setting.max_user_tokens')}
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('企业账户最大子账户数量')}
                  field={'sub_account_setting.max_count'}
                  step={1}
                  min={1}
                  extraText={t(
                    '每个已通过企业认证的账户最多可创建的子账户数量，默认 10',
                  )}
                  placeholder={'10'}
                  onChange={handleFieldChange('sub_account_setting.max_count')}
                />
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存通用设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
