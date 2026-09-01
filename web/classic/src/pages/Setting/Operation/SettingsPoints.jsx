import React, { useEffect, useState, useRef } from 'react';
import {
  Button,
  Col,
  Form,
  Row,
  Spin,
  Typography,
  Banner,
} from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import ChannelPointsRewards from './components/ChannelPointsRewards';

export default function SettingsPoints(props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    'points_setting.enabled': false,
    'points_setting.require_kyc': true,
    'points_setting.quota_per_point': 684.93,
    'points_setting.kyc_verified_points': 0,
    'points_setting.kyc_inviter_points': 0,
    'points_setting.new_user_points': 0,
    'points_setting.enabled_channels': [],
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);
  const [channelOptions, setChannelOptions] = useState([]);

  // 渠道下拉：让人选渠道名而不是背 ID。取法与使用日志的渠道筛选一致
  // （useUsageLogsData.jsx:120）。
  useEffect(() => {
    let cancelled = false;
    API.get('/api/channel/?p=0&page_size=1000')
      .then((res) => {
        if (cancelled) return;
        const { success, data } = res.data || {};
        const items = success
          ? Array.isArray(data?.items)
            ? data.items
            : Array.isArray(data)
              ? data
              : []
          : [];
        setChannelOptions(
          items.map((c) => ({
            label: `${c.id} - ${c.name || '未命名'}`,
            value: c.id,
          })),
        );
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  function handleFieldChange(fieldName) {
    return (value) => {
      setInputs((inputs) => ({ ...inputs, [fieldName]: value }));
    };
  }

  function onSubmit() {
    // compareObjects 用 !== 逐个比属性，数组永远判为「已变」（引用不同）。
    // 这个表单此前全是标量字段所以没暴露；enabled_channels 是第一个数组字段，
    // 不额外按值比的话，每次保存都会多发一次同值的 PUT，而且「你似乎并没有修改
    // 什么」的提示从此再不会出现——用户改错了什么也得不到反馈。
    //
    // 排序后比较：渠道的先后顺序不影响语义，不排的话调换选择顺序会被误判为修改。
    const sameArray = (a, b) =>
      Array.isArray(a) &&
      Array.isArray(b) &&
      JSON.stringify([...a].sort()) === JSON.stringify([...b].sort());
    const updateArray = compareObjects(inputs, inputsRow).filter(
      (item) => !sameArray(inputs[item.key], inputsRow[item.key]),
    );
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      // 数组字段必须发 JSON：后端 config.updateConfigFromMap 对 Slice 走
      // json.Unmarshal，String([1,18]) 得到的 "1,18" 解析失败后是**静默 continue**，
      // 表现为点了保存、提示成功、配置却没变。
      const raw = inputs[item.key];
      const value = Array.isArray(raw) ? JSON.stringify(raw) : String(raw);
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
        // 数组字段存的是 JSON 串，直接塞给 Select 会渲染成一个字符串选项
        if (Array.isArray(inputs[key])) {
          try {
            const parsed = JSON.parse(props.options[key] || '[]');
            currentInputs[key] = Array.isArray(parsed) ? parsed : [];
          } catch {
            currentInputs[key] = [];
          }
          continue;
        }
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
                  label={t('未实名用户不参加积分活动')}
                  extraText={t(
                    '只卡发放：签到、邀请人赠分需先实名；已到账的积分随时可花',
                  )}
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
                  extraText={t(
                    '1 积分 = 1 分钱时约为 684.93，上线后不建议修改',
                  )}
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
                  field={'points_setting.new_user_points'}
                  label={t('新用户注册赠送积分')}
                  extraText={t('0 = 关闭；注册即到账，不受实名开关约束')}
                  onChange={handleFieldChange('points_setting.new_user_points')}
                  min={0}
                  disabled={!inputs['points_setting.enabled']}
                />
              </Col>
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
                <Form.Select
                  field={'points_setting.enabled_channels'}
                  label={t('允许积分抵扣的渠道')}
                  placeholder={t('留空 = 不限制渠道')}
                  multiple
                  filter
                  style={{ width: '100%' }}
                  optionList={channelOptions}
                  onChange={handleFieldChange(
                    'points_setting.enabled_channels',
                  )}
                  disabled={!inputs['points_setting.enabled']}
                  extraText={t(
                    '积分该不该抵，取决于这次调用走了哪条供应链：自建渠道花的是自己的算力，外采渠道花的是供应商账单。分组合并后同一分组下两种渠道并存，只靠分组白名单已经分不开。留空表示不做渠道限制（与本功能上线前行为一致）。',
                  )}
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={24} md={16} lg={16} xl={16}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text strong>
                    {t('允许积分抵扣的分组白名单')}
                  </Typography.Text>
                </div>
                <div className='flex flex-wrap items-center gap-2'>
                  <Typography.Text type='tertiary' size='small'>
                    {t(
                      '已迁移到「分组管理 → 充值 · 限流 · 积分」，与该分组的其他配置放在一起。',
                    )}
                  </Typography.Text>
                  <Button
                    size='small'
                    theme='borderless'
                    onClick={() => navigate('/console/group')}
                  >
                    {t('前往分组管理')}
                  </Button>
                </div>
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

      {/*
        放在积分设置之后、且不在那个 Form 里：它自己管加载与保存（单独一个 JSON
        option key），塞进上面的 compareObjects 批量提交会把数组当标量比对。
      */}
      <ChannelPointsRewards
        options={props.options}
        refresh={props.refresh}
        disabled={!inputs['points_setting.enabled']}
      />
    </>
  );
}
