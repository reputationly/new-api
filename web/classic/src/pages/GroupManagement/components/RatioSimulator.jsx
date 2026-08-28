import React, { useState, useCallback } from 'react';
import {
  Button,
  Card,
  Col,
  Input,
  Row,
  Select,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconPlay } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError } from '../../../helpers';

const { Text } = Typography;

/**
 * 倍率试算器。
 *
 * 两层解析叠加通配的可解释性必须有工具兜底，否则运营改完价不敢上线——尤其配过
 * 「定价 =」之后，它会把针对用户分组的身份折扣整个吃掉，只有把每一层摊开才看得见。
 *
 * 刻意走后端 /api/group/resolve 而不是在前端复算：前端复算一份就意味着有两个
 * 实现，一旦分叉，试算器就从「敢不敢上线的依据」变成误导源。
 */
export default function RatioSimulator({ groupNames = [] }) {
  const { t } = useTranslation();
  const [userGroup, setUserGroup] = useState('');
  const [usingGroup, setUsingGroup] = useState('');
  const [modelName, setModelName] = useState('');
  const [result, setResult] = useState(null);
  const [loading, setLoading] = useState(false);

  const run = useCallback(async () => {
    if (!usingGroup) {
      showError(t('请选择令牌分组'));
      return;
    }
    setLoading(true);
    try {
      const res = await API.post('/api/group/resolve', {
        user_group: userGroup,
        using_group: usingGroup,
        model_name: modelName,
      });
      if (res.data?.success) {
        setResult(res.data.data);
      } else {
        showError(res.data?.message || t('试算失败'));
      }
    } catch (e) {
      showError(t('试算失败'));
    } finally {
      setLoading(false);
    }
  }, [userGroup, usingGroup, modelName, t]);

  const groupOptions = groupNames.map((g) => ({ label: g, value: g }));

  const renderLayer = (label, hit, detail, value) => (
    <Row className='py-1' gutter={8}>
      <Col span={7}>
        <Text type='tertiary' size='small'>
          {label}
        </Text>
      </Col>
      <Col span={11}>
        {hit ? (
          <Text size='small'>{detail}</Text>
        ) : (
          <Text type='tertiary' size='small'>
            {t('未命中')}
          </Text>
        )}
      </Col>
      <Col span={6} className='text-right'>
        <Text strong={hit} type={hit ? undefined : 'tertiary'}>
          {value}
        </Text>
      </Col>
    </Row>
  );

  return (
    <Card
      title={t('倍率试算器')}
      headerExtraContent={
        <Text type='tertiary' size='small'>
          {t('改完价先在这里确认一遍再上线')}
        </Text>
      }
    >
      <Row gutter={12} className='mb-3'>
        <Col xs={24} sm={7}>
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t('用户分组')}
          </Text>
          <Select
            data-testid='sim-user-group'
            style={{ width: '100%' }}
            placeholder={t('（不限）')}
            value={userGroup || null}
            optionList={groupOptions}
            onChange={setUserGroup}
            showClear
            filter
          />
        </Col>
        <Col xs={24} sm={7}>
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t('令牌分组')}
          </Text>
          <Select
            data-testid='sim-using-group'
            style={{ width: '100%' }}
            placeholder={t('必选')}
            value={usingGroup || null}
            optionList={groupOptions}
            onChange={setUsingGroup}
            filter
          />
        </Col>
        <Col xs={24} sm={7}>
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t('模型')}
          </Text>
          <Input
            data-testid='sim-model'
            placeholder={t('如 GLM-5')}
            value={modelName}
            onChange={setModelName}
          />
        </Col>
        <Col xs={24} sm={3}>
          <Text type='tertiary' size='small' className='mb-1 block'>
            &nbsp;
          </Text>
          <Button
            data-testid='sim-run'
            icon={<IconPlay />}
            theme='solid'
            loading={loading}
            onClick={run}
            style={{ width: '100%' }}
          >
            {t('试算')}
          </Button>
        </Col>
      </Row>

      {result && (
        <div
          data-testid='sim-result'
          className='rounded-lg bg-[var(--semi-color-fill-0)] p-3'
        >
          {!result.usable && (
            <Tag color='orange' shape='circle' className='mb-2'>
              {t('该用户分组当前用不到这个令牌分组')}
            </Tag>
          )}
          {renderLayer(
            t('分组基础倍率'),
            true,
            usingGroup,
            `${result.group_ratio}x`,
          )}
          {renderLayer(
            t('用户身份折扣'),
            result.has_special_ratio,
            t('{{u}} 使用 {{g}} 时覆盖', { u: userGroup, g: usingGroup }),
            result.has_special_ratio ? `${result.special_ratio}x` : '—',
          )}
          {renderLayer(
            t('模型折扣'),
            !!result.rule_match,
            result.rule_match
              ? `${result.rule_match} · ${
                  result.rule_mode === 'override' ? t('定价 =') : t('折扣 ×')
                } ${result.rule_value}`
              : '',
            result.rule_match
              ? result.rule_mode === 'override'
                ? `${result.rule_value}x`
                : `×${result.rule_value}`
              : '—',
          )}
          {renderLayer(
            t('用户档折扣'),
            !!result.user_rule_match,
            result.user_rule_match
              ? `${userGroup} · ${result.user_rule_match} · ${t('折扣 ×')} ${
                  result.user_rule_value
                }`
              : '',
            result.user_rule_match ? `×${result.user_rule_value}` : '—',
          )}
          <div className='mt-2 flex items-center justify-between border-t pt-2'>
            <Text strong>{t('最终倍率')}</Text>
            <Text strong style={{ fontSize: 18 }}>
              {Number(result.final.toFixed(6))}x
            </Text>
          </div>
          {result.rule_mode === 'override' && result.has_special_ratio && (
            <Text type='warning' size='small' className='mt-2 block'>
              {t(
                '注意：「定价 =」已覆盖用户身份折扣（{{base}}x），身份折扣在本次计费中不生效。',
                { base: result.special_ratio },
              )}
            </Text>
          )}
        </div>
      )}
    </Card>
  );
}
