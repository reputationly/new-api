import React, { useCallback, useMemo, useRef } from 'react';
import {
  Button,
  Empty,
  InputNumber,
  Select,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
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
 * 用户档的三项「散配置」：充值倍率、请求速率限制、积分抵扣白名单。
 * 三者都按 user.group 索引，与令牌选了哪条线路无关。
 *
 * 它们原本分别住在支付设置、速率限制设置、运营设置三个页面，各自一个 JSON 文本框。
 * 这里按用户档一行铺开——同一个档的所有配置摆在一起才看得出「这个档是什么待遇」。
 * 行的候选 = 线路名 ∪ 已配过档位折扣的用户档 ∪ 三项配置里已出现的名字：
 * 用户档与线路共用名字空间，绝大多数用户档都有一条同名线路；谈判档没有同名线路，
 * 靠后两项并进来，不必为了配充值倍率先建一条占位线路。
 *
 * 只搬 UI 不搬存储：三项仍然写回各自原本的 option key。
 */
export default function GroupExtraSettings({
  inputs,
  groupNames = [],
  tierNames = [],
  onChange,
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

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
  // 就会换身份，columns 的 useMemo 跟着重建，Semi Table 每敲一个字重建一遍单元格。
  //
  // 这张表现在全是 InputNumber，实测它在这种情况下光标**不会**跳
  // （对照实验：同样条件下 Input 跳到末尾、InputNumber 不动），所以当下只是白费
  // 渲染。留住这个写法是因为一旦这里加一个文本输入框（比如给分组配个备注），
  // 症状会立刻变成 GroupTable.jsx 与 ModelRatioEditor.jsx 已经踩过两次的光标跳。
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

  const rowNames = useMemo(
    () =>
      Array.from(
        new Set([
          ...groupNames,
          ...tierNames,
          ...Object.keys(topup),
          ...Object.keys(rateLimit),
        ]),
      ).sort(),
    [groupNames, tierNames, topup, rateLimit],
  );

  const rows = useMemo(
    () =>
      rowNames.map((name) => ({
        name,
        topupRatio: topup[name],
        limitTotal: Array.isArray(rateLimit[name])
          ? rateLimit[name][0]
          : undefined,
        limitSuccess: Array.isArray(rateLimit[name])
          ? rateLimit[name][1]
          : undefined,
      })),
    [rowNames, topup, rateLimit],
  );

  const columns = useMemo(
    () => [
      {
        title: t('用户档'),
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

  if (!rowNames.length) {
    return <Empty description={t('请先在「线路」标签页创建分组')} />;
  }

  return (
    <div>
      <Text type='tertiary' size='small' className='mb-3 block'>
        {t(
          '充值倍率决定该档用户充值时的到账比例；速率限制留空表示不限，配置后优先级高于全局限制，限制周期沿用「速率限制设置」里的全局周期。',
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
      {/*
        总开关在这里**只读**。它归「运营设置 → 积分设置」管，控制的东西远不止分组
        （抵扣比例、KYC 要求、赠送积分…）。两个页面都能改同一个 key 的话，
        改完这边、那边还开着旧状态，谁后保存谁生效——这种双写不值得为省一次跳转而留。
      */}
      <div className='mb-2 flex flex-wrap items-center gap-2'>
        <Switch checked={!!inputs['points_setting.enabled']} disabled />
        <Text type={inputs['points_setting.enabled'] ? undefined : 'tertiary'}>
          {inputs['points_setting.enabled']
            ? t('积分抵扣已启用')
            : t('积分抵扣未启用，白名单不生效')}
        </Text>
        <Button
          size='small'
          theme='borderless'
          onClick={() => navigate('/console/setting?tab=operation')}
        >
          {t('去运营设置开关')}
        </Button>
      </div>
      <Text type='tertiary' size='small' className='mb-2 block'>
        {t('留空 = 所有用户档只扣余额。采购档零配置即安全。')}
      </Text>
      <Select
        multiple
        filter
        placeholder={t('选择允许积分抵扣的用户档')}
        value={pointsGroups}
        disabled={!inputs['points_setting.enabled']}
        // 候选是线路名并入已选值：已选里可能有历史上手输、现已不存在的名字，
        // 不并进来的话它们显示不出来、也删不掉
        optionList={Array.from(new Set([...rowNames, ...pointsGroups])).map(
          (g) => ({ label: g, value: g }),
        )}
        onChange={(arr) =>
          onChange('points_setting.enabled_groups', JSON.stringify(arr || []))
        }
        style={{ width: '100%', maxWidth: 640 }}
      />
    </div>
  );
}
