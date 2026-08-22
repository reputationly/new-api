import React, { useCallback, useMemo, useRef } from 'react';
import {
  Empty,
  InputNumber,
  Switch,
  TagInput,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import CardTable from '../../../components/common/ui/CardTable';

const { Text, Title } = Typography;

function parseJSON(str, fallback) {
  if (!str || !str.trim()) return fallback;
  try {
    return JSON.parse(str);
  } catch {
    return fallback;
  }
}

/**
 * 分组的三项「散配置」：充值倍率、请求速率限制、积分抵扣白名单。
 *
 * 它们原本分别住在支付设置、速率限制设置、运营设置三个页面，各自一个 JSON 文本框。
 * 这里按分组一行铺开——同一个分组的所有配置摆在一起才看得出「这个分组是什么档位」。
 *
 * 只搬 UI 不搬存储：三项仍然写回各自原本的 option key。
 */
export default function GroupExtraSettings({
  inputs,
  groupNames = [],
  onChange,
}) {
  const { t } = useTranslation();

  const topup = useMemo(
    () => parseJSON(inputs.TopupGroupRatio, {}),
    [inputs.TopupGroupRatio],
  );
  const rateLimit = useMemo(
    () => parseJSON(inputs.ModelRequestRateLimitGroup, {}),
    [inputs.ModelRequestRateLimitGroup],
  );
  const pointsGroups = useMemo(
    () => parseJSON(inputs['points_setting.enabled_groups'], []),
    [inputs['points_setting.enabled_groups']],
  );

  // 三个 ref 都是「渲染期同步、事件回调里读」，**不能**进 useCallback 依赖。
  //
  // topup / rateLimit 每次敲键都会变——它们由 parseJSON(inputs.xxx) 算出，而
  // inputs 正是被本次 onChange 刚更新过的那份。写进依赖，setTopup / setRateLimit
  // 就会换身份，columns 的 useMemo 跟着重建，Semi Table 重建单元格，
  // InputNumber 光标跳到末尾。同一个坑 GroupTable.jsx 与 ModelRatioEditor.jsx
  // 各踩过一次，注释都在。
  const topupRef = useRef(topup);
  topupRef.current = topup;
  const rateLimitRef = useRef(rateLimit);
  rateLimitRef.current = rateLimit;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const setTopup = useCallback((name, value) => {
    const next = { ...topupRef.current };
    if (value === null || value === undefined) {
      delete next[name];
    } else {
      next[name] = value;
    }
    onChangeRef.current('TopupGroupRatio', JSON.stringify(next, null, 2));
  }, []);

  const setRateLimit = useCallback((name, index, value) => {
    const next = { ...rateLimitRef.current };
    const current = Array.isArray(next[name]) ? [...next[name]] : [0, 0];
    current[index] = value ?? 0;
    // 两个值都归零视为「不限」，直接摘掉这条，避免留下一条语义暧昧的 [0,0]
    if (current[0] === 0 && current[1] === 0) {
      delete next[name];
    } else {
      next[name] = current;
    }
    onChangeRef.current(
      'ModelRequestRateLimitGroup',
      JSON.stringify(next, null, 2),
    );
  }, []);

  const rows = useMemo(
    () =>
      groupNames.map((name) => ({
        name,
        topupRatio: topup[name],
        limitTotal: Array.isArray(rateLimit[name])
          ? rateLimit[name][0]
          : undefined,
        limitSuccess: Array.isArray(rateLimit[name])
          ? rateLimit[name][1]
          : undefined,
      })),
    [groupNames, topup, rateLimit],
  );

  const columns = useMemo(
    () => [
      {
        title: t('分组'),
        dataIndex: 'name',
        key: 'name',
        width: 160,
        render: (v) => <Text strong>{v}</Text>,
      },
      {
        title: t('充值倍率'),
        key: 'topup',
        width: 160,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            step={0.1}
            style={{ width: '100%' }}
            placeholder={t('默认 1')}
            value={record.topupRatio}
            onChange={(v) => setTopup(record.name, v)}
          />
        ),
      },
      {
        title: t('每周期最多请求'),
        key: 'limit_total',
        width: 170,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            style={{ width: '100%' }}
            placeholder={t('不限')}
            value={record.limitTotal}
            onChange={(v) => setRateLimit(record.name, 0, v)}
          />
        ),
      },
      {
        title: t('每周期最多成功'),
        key: 'limit_success',
        width: 170,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            style={{ width: '100%' }}
            placeholder={t('不限')}
            value={record.limitSuccess}
            onChange={(v) => setRateLimit(record.name, 1, v)}
          />
        ),
      },
    ],
    [t, setTopup, setRateLimit],
  );

  if (!groupNames.length) {
    return <Empty description={t('请先在「分组」标签页创建分组')} />;
  }

  return (
    <div>
      <Text type='tertiary' size='small' className='mb-3 block'>
        {t(
          '充值倍率决定该分组用户充值时的到账比例；速率限制留空表示不限，配置后优先级高于全局限制，限制周期沿用「速率限制设置」里的全局周期。',
        )}
      </Text>

      <CardTable
        columns={columns}
        dataSource={rows}
        rowKey='name'
        hidePagination
        size='small'
      />

      <Title heading={6} className='mb-1 mt-6'>
        {t('积分抵扣白名单')}
      </Title>
      <div className='mb-2 flex items-center gap-2'>
        <Switch
          checked={!!inputs['points_setting.enabled']}
          onChange={(v) => onChange('points_setting.enabled', v)}
        />
        <Text>{t('启用积分抵扣')}</Text>
      </div>
      <Text type='tertiary' size='small' className='mb-2 block'>
        {t('留空 = 所有分组只扣余额。采购分组零配置即安全。')}
      </Text>
      <TagInput
        placeholder={t('输入分组名后回车')}
        value={pointsGroups}
        disabled={!inputs['points_setting.enabled']}
        onChange={(arr) =>
          onChange('points_setting.enabled_groups', JSON.stringify(arr))
        }
        style={{ width: '100%', maxWidth: 640 }}
      />
    </div>
  );
}
