import React, { useMemo } from 'react';
import { Empty, Tag, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import CardTable from '../../../components/common/ui/CardTable';

const { Text } = Typography;

/**
 * 用户档 → 最终可用线路。
 *
 * 可用性由五个来源合成（全局「用户可选」、用户自身同名线路、可用线路增减规则、
 * 停用态、auto 池），运营对着规则推不出结果。这里只展示后端算好的**结果**
 * （/api/group/overview 的 usable_matrix），规则在下方单独改。
 *
 * 只读、按已保存的数据渲染：改了规则要先保存才会更新——这是刻意的，让「保存前
 * 的规则」与「保存后的结果」有明确边界，避免把未保存的推演当成已生效。
 */
export default function TierUsableMatrix({ matrix = {} }) {
  const { t } = useTranslation();

  const rows = useMemo(
    () =>
      Object.keys(matrix)
        .sort()
        .map((tier) => ({ tier, lines: matrix[tier] || [] })),
    [matrix],
  );

  const columns = useMemo(
    () => [
      {
        title: t('用户档'),
        dataIndex: 'tier',
        key: 'tier',
        width: 180,
        render: (v) => <Text strong>{v}</Text>,
      },
      {
        title: t('可用线路（已保存的最终结果）'),
        dataIndex: 'lines',
        key: 'lines',
        render: (lines, record) =>
          lines.length ? (
            <div className='flex flex-wrap gap-1'>
              {lines.map((l) => (
                <Tag
                  key={l}
                  size='small'
                  shape='circle'
                  color={
                    l === 'auto'
                      ? 'blue'
                      : l === record.tier
                        ? 'green'
                        : 'light-blue'
                  }
                >
                  {l}
                </Tag>
              ))}
            </div>
          ) : (
            <Text type='danger' size='small'>
              {t('没有任何可用线路，该档用户创建令牌时无线路可选')}
            </Text>
          ),
      },
    ],
    [t],
  );

  if (!rows.length) {
    return (
      <Empty description={t('保存后这里会显示每个用户档实际可用的线路')} />
    );
  }

  return (
    <div>
      <CardTable
        columns={columns}
        dataSource={rows}
        rowKey='tier'
        hidePagination
        size='small'
      />
      <Text type='tertiary' size='small' className='mt-2 block'>
        {t(
          '绿色 = 与用户档同名的线路（始终可用）；蓝色 = auto。已停用的线路不会出现。',
        )}
      </Text>
    </div>
  );
}
