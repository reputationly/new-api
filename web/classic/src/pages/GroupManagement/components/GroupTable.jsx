import React, {
  useState,
  useCallback,
  useMemo,
  useRef,
  useEffect,
} from 'react';
import {
  Button,
  Input,
  InputNumber,
  Checkbox,
  Modal,
  Tag,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import { IconPlus, IconDelete } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API } from '../../../helpers';
import CardTable from '../../../components/common/ui/CardTable';

const { Text } = Typography;

const STATUS_META = {
  ok: { color: 'green', label: '正常' },
  no_channel: {
    color: 'red',
    label: '无渠道挂载',
    hint: '该分组没有任何启用渠道，用户一旦选中必然报「无可用渠道」。去渠道管理把渠道挂到这个分组上。',
  },
  unreachable: {
    color: 'orange',
    label: '无人可用',
    hint: '该分组有渠道，但既没勾「用户可选」、也没有用户属于它、不在自动分组池里、也没有特殊可用规则追加——没有任何路径能让用户用上它。',
  },
};

let _idCounter = 0;
const uid = () => `gr_${++_idCounter}`;

function parseJSON(str, fallback) {
  if (!str || !str.trim()) return fallback;
  try {
    return JSON.parse(str);
  } catch {
    return fallback;
  }
}

function buildRows(groupRatioStr, userUsableGroupsStr) {
  const ratioMap = parseJSON(groupRatioStr, {});
  const usableMap = parseJSON(userUsableGroupsStr, {});

  const allNames = new Set([
    ...Object.keys(ratioMap),
    ...Object.keys(usableMap),
  ]);

  return Array.from(allNames).map((name) => ({
    _id: uid(),
    name,
    ratio: ratioMap[name] ?? 1,
    selectable: name in usableMap,
    description: usableMap[name] ?? '',
  }));
}

export function serializeGroupTable(rows) {
  const groupRatio = {};
  const userUsableGroups = {};

  rows.forEach((row) => {
    if (!row.name) return;
    groupRatio[row.name] = row.ratio;
    if (row.selectable) {
      userUsableGroups[row.name] = row.description;
    }
  });

  return {
    GroupRatio: JSON.stringify(groupRatio, null, 2),
    UserUsableGroups: JSON.stringify(userUsableGroups, null, 2),
  };
}

export default function GroupTable({
  groupRatio,
  userUsableGroups,
  health = {},
  onChange,
  onSelectGroup,
  seedNames,
}) {
  const { t } = useTranslation();

  const [rows, setRows] = useState(() =>
    buildRows(groupRatio, userUsableGroups),
  );

  // Use functional setRows to keep updateRow/addRow/removeRow referentially
  // stable, preventing columns useMemo from rebuilding on every keystroke
  // which causes the Input cursor to jump to end (cursor reset bug).
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const emitAndSet = useCallback((updater) => {
    setRows((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater;
      onChangeRef.current?.(serializeGroupTable(next));
      return next;
    });
  }, []);

  const updateRow = useCallback(
    (id, field, value) => {
      emitAndSet((prev) =>
        prev.map((r) => (r._id === id ? { ...r, [field]: value } : r)),
      );
    },
    [emitAndSet],
  );

  const addRow = useCallback(() => {
    emitAndSet((prev) => {
      const existingNames = new Set(prev.map((r) => r.name));
      let counter = 1;
      let newName = `group_${counter}`;
      while (existingNames.has(newName)) {
        counter++;
        newName = `group_${counter}`;
      }
      return [
        ...prev,
        {
          _id: uid(),
          name: newName,
          ratio: 1,
          selectable: true,
          description: '',
        },
      ];
    });
  }, [emitAndSet]);

  const removeRow = useCallback(
    (id) => {
      emitAndSet((prev) => prev.filter((r) => r._id !== id));
    },
    [emitAndSet],
  );

  const groupNames = useMemo(() => rows.map((r) => r.name), [rows]);

  const duplicateNames = useMemo(() => {
    const counts = {};
    groupNames.forEach((n) => {
      counts[n] = (counts[n] || 0) + 1;
    });
    return new Set(Object.keys(counts).filter((k) => counts[k] > 1));
  }, [groupNames]);

  // Use ref so column render functions always read the latest duplicate set
  // without adding duplicateNames to columns deps (which would break cursor).
  const duplicateNamesRef = useRef(duplicateNames);
  duplicateNamesRef.current = duplicateNames;

  const healthRef = useRef(health);
  healthRef.current = health;
  const onSelectGroupRef = useRef(onSelectGroup);
  onSelectGroupRef.current = onSelectGroup;

  // 「一键补建」：把被渠道引用但没配置的分组按缺省值建出来。
  // 现网大概率已经存在这类失配，这是存量对齐的入口。
  const seedRef = useRef(null);
  useEffect(() => {
    if (!seedNames || seedNames === seedRef.current) return;
    seedRef.current = seedNames;
    if (!seedNames.length) return;
    emitAndSet((prev) => {
      const existing = new Set(prev.map((r) => r.name));
      const added = seedNames
        .filter((n) => !existing.has(n))
        .map((name) => ({
          _id: uid(),
          name,
          ratio: 1,
          selectable: false,
          description: '',
        }));
      return added.length ? [...prev, ...added] : prev;
    });
  }, [seedNames, emitAndSet]);

  /**
   * 删除前先查引用。分组被 user / token / channel / subscription_plan 四处引用，
   * 从配置里抹掉一行不会有任何提示，用户会撞上「分组已被弃用」而管理员不知情。
   */
  const confirmRemove = useCallback(
    async (record) => {
      let refs = null;
      try {
        const res = await API.get(
          `/api/group/references?group=${encodeURIComponent(record.name)}`,
        );
        if (res.data?.success) refs = res.data.data;
      } catch (e) {
        // 查不到不阻断，但要让管理员知道这一步没做成
      }

      if (!refs) {
        Modal.confirm({
          title: t('无法确认引用情况'),
          type: 'warning',
          content: t('引用检查失败，无法判断删除该分组会影响谁。仍要删除吗？'),
          onOk: () => removeRow(record._id),
        });
        return;
      }

      if (refs.total === 0) {
        removeRow(record._id);
        return;
      }

      Modal.confirm({
        title: t('分组 {{name}} 仍被引用', { name: record.name }),
        type: 'warning',
        content: (
          <div className='text-sm leading-7'>
            <div>{t('删除后这些引用会指向一个不存在的分组：')}</div>
            <ul className='ml-4 list-disc'>
              {refs.users > 0 && (
                <li>{t('用户 {{n}} 个', { n: refs.users })}</li>
              )}
              {refs.tokens > 0 && (
                <li>{t('令牌 {{n}} 个', { n: refs.tokens })}</li>
              )}
              {refs.channels > 0 && (
                <li>{t('渠道 {{n}} 个', { n: refs.channels })}</li>
              )}
              {refs.plans > 0 && (
                <li>{t('订阅计划 {{n}} 个', { n: refs.plans })}</li>
              )}
            </ul>
            <div className='mt-2'>
              {t('这些用户和令牌会立刻收到「分组已被弃用」。确定要删除吗？')}
            </div>
          </div>
        ),
        okText: t('仍然删除'),
        okButtonProps: { type: 'danger' },
        onOk: () => removeRow(record._id),
      });
    },
    [t, removeRow],
  );

  const columns = useMemo(
    () => [
      {
        title: t('分组名称'),
        dataIndex: 'name',
        key: 'name',
        width: 180,
        render: (_, record) => (
          <Input
            size='small'
            value={record.name}
            status={
              duplicateNamesRef.current.has(record.name) ? 'warning' : undefined
            }
            onChange={(v) => updateRow(record._id, 'name', v)}
          />
        ),
      },
      {
        title: t('倍率'),
        dataIndex: 'ratio',
        key: 'ratio',
        width: 120,
        render: (_, record) => (
          <div>
            <InputNumber
              size='small'
              min={0}
              step={0.1}
              value={record.ratio}
              style={{ width: '100%' }}
              onChange={(v) => updateRow(record._id, 'ratio', v ?? 0)}
            />
            {healthRef.current[record.name]?.rule_count > 0 && (
              <Text type='tertiary' size='small'>
                {t('基准')}
              </Text>
            )}
          </div>
        ),
      },
      {
        title: t('状态'),
        key: 'status',
        width: 110,
        render: (_, record) => {
          const h = healthRef.current[record.name];
          if (!h) {
            return (
              <Tag size='small' color='grey' shape='circle'>
                {t('未保存')}
              </Tag>
            );
          }
          const meta = STATUS_META[h.status] || STATUS_META.ok;
          const tag = (
            <Tag size='small' color={meta.color} shape='circle'>
              {t(meta.label)}
            </Tag>
          );
          return meta.hint ? (
            <Tooltip content={t(meta.hint)} position='top'>
              {tag}
            </Tooltip>
          ) : (
            tag
          );
        },
      },
      {
        title: t('渠道/模型'),
        key: 'coverage',
        width: 100,
        render: (_, record) => {
          const h = healthRef.current[record.name];
          if (!h) return <Text type='tertiary'>-</Text>;
          return (
            <Text type={h.channel_count === 0 ? 'danger' : undefined}>
              {h.channel_count} / {h.model_count}
            </Text>
          );
        },
      },
      {
        title: t('模型折扣'),
        key: 'rules',
        width: 130,
        render: (_, record) => {
          const h = healthRef.current[record.name];
          const count = h?.rule_count || 0;
          const stale = h?.stale_rules || [];
          return (
            <div className='flex items-center gap-1'>
              <Button
                theme='borderless'
                size='small'
                onClick={() => onSelectGroupRef.current?.(record.name)}
              >
                {count > 0 ? t('{{n}} 条', { n: count }) : t('配置')}
              </Button>
              {stale.length > 0 && (
                <Tooltip
                  content={t(
                    '这些规则匹配不到本分组的任何模型，当前不生效：{{list}}',
                    {
                      list: stale.join(', '),
                    },
                  )}
                >
                  <Tag size='small' color='orange' shape='circle'>
                    {t('{{n}} 条未生效', { n: stale.length })}
                  </Tag>
                </Tooltip>
              )}
            </div>
          );
        },
      },
      {
        title: t('用户可选'),
        dataIndex: 'selectable',
        key: 'selectable',
        width: 90,
        align: 'center',
        render: (_, record) => (
          <Checkbox
            checked={record.selectable}
            onChange={(e) =>
              updateRow(record._id, 'selectable', e.target.checked)
            }
          />
        ),
      },
      {
        title: t('描述'),
        dataIndex: 'description',
        key: 'description',
        render: (_, record) =>
          record.selectable ? (
            <Input
              size='small'
              value={record.description}
              placeholder={t('分组描述')}
              onChange={(v) => updateRow(record._id, 'description', v)}
            />
          ) : (
            <Text type='tertiary' size='small'>
              -
            </Text>
          ),
      },
      {
        title: '',
        key: 'actions',
        width: 50,
        render: (_, record) => (
          <Button
            icon={<IconDelete />}
            type='danger'
            theme='borderless'
            size='small'
            onClick={() => confirmRemove(record)}
          />
        ),
      },
    ],
    [t, updateRow, confirmRemove],
  );

  return (
    <div>
      <CardTable
        columns={columns}
        dataSource={rows}
        rowKey='_id'
        hidePagination
        size='small'
        empty={<Text type='tertiary'>{t('暂无分组，点击下方按钮添加')}</Text>}
      />
      <div className='mt-3 flex justify-center'>
        <Button icon={<IconPlus />} theme='outline' onClick={addRow}>
          {t('添加分组')}
        </Button>
      </div>
      {duplicateNames.size > 0 && (
        <Text type='warning' size='small' className='mt-2 block'>
          {t('存在重复的分组名称：')}
          {Array.from(duplicateNames).join(', ')}
        </Text>
      )}
    </div>
  );
}
