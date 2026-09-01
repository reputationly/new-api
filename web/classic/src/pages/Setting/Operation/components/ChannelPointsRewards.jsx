import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Empty,
  Form,
  Modal,
  Popconfirm,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, copy, showError, showSuccess } from '../../../../helpers';

const { Text } = Typography;

const OPTION_KEY = 'points_setting.channel_rewards';

/** 该渠道商的专属邀请链接，格式与个人中心的邀请卡片一致（topup/index.jsx:874） */
const affLinkOf = (affCode) =>
  affCode ? `${window.location.origin}/register?aff=${affCode}` : '';

function parseRules(raw) {
  if (!raw || !String(raw).trim()) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

/**
 * 渠道积分奖励：按邀请人覆盖「新用户注册赠送积分」。
 *
 * 独立成组件而不是塞进 SettingsPoints：那边是「一堆标量字段 + compareObjects 批量
 * 提交」的模式，这里是一张需要搜用户、发链接、单独存 JSON 的表，混在一起会让两套
 * 状态互相污染。它自己管自己的加载与保存。
 */
export default function ChannelPointsRewards({ options, refresh, disabled }) {
  const { t } = useTranslation();
  const [rules, setRules] = useState([]);
  const [saving, setSaving] = useState(false);
  const [modalVisible, setModalVisible] = useState(false);
  const [userOptions, setUserOptions] = useState([]);
  const [searching, setSearching] = useState(false);
  const [draft, setDraft] = useState(null);

  useEffect(() => {
    setRules(parseRules(options?.[OPTION_KEY]));
  }, [options]);

  // 已配过的邀请人不再出现在搜索结果里：后端 CheckChannelPointsRewards 会拒绝重复
  // 的 inviter_id，与其让人选完了才在保存时报错，不如根本选不到。
  const configuredIds = useMemo(
    () => new Set(rules.map((r) => r.inviter_id)),
    [rules],
  );

  const searchUsers = useCallback(
    async (keyword) => {
      if (!keyword || !keyword.trim()) {
        setUserOptions([]);
        return;
      }
      setSearching(true);
      try {
        const res = await API.get(
          `/api/user/search?keyword=${encodeURIComponent(keyword)}&p=1&page_size=20`,
        );
        const items = res.data?.data?.items || res.data?.data || [];
        setUserOptions(
          (Array.isArray(items) ? items : [])
            .filter((u) => !configuredIds.has(u.id))
            .map((u) => ({
              label: `${u.username} (ID ${u.id})`,
              value: u.id,
              username: u.username,
              affCode: u.aff_code,
            })),
        );
      } catch {
        setUserOptions([]);
      } finally {
        setSearching(false);
      }
    },
    [configuredIds],
  );

  const persist = useCallback(
    async (next) => {
      setSaving(true);
      try {
        const res = await API.put('/api/option/', {
          key: OPTION_KEY,
          value: JSON.stringify(next),
        });
        if (res.data?.success) {
          showSuccess(t('渠道积分奖励已保存'));
          setRules(next);
          refresh?.();
        } else {
          showError(res.data?.message || t('保存失败'));
        }
      } catch {
        showError(t('保存失败，请重试'));
      } finally {
        setSaving(false);
      }
    },
    [refresh, t],
  );

  const addRule = useCallback(() => {
    if (!draft?.inviter_id) {
      showError(t('请先选择渠道商用户'));
      return;
    }
    persist([...rules, { ...draft, enabled: true }]);
    setModalVisible(false);
    setDraft(null);
  }, [draft, rules, persist, t]);

  const columns = useMemo(
    () => [
      {
        title: t('渠道商'),
        dataIndex: 'username',
        render: (text, record) => (
          <div>
            <div>{text || '-'}</div>
            <Text type='tertiary' size='small'>
              ID {record.inviter_id}
            </Text>
          </div>
        ),
      },
      {
        title: t('奖励积分'),
        dataIndex: 'points',
        width: 120,
        render: (v) =>
          v > 0 ? (
            <Tag color='green' shape='circle'>
              {v}
            </Tag>
          ) : (
            // 0 是「该渠道不送」，与默认值无关——列表上必须一眼看出来，
            // 否则会被当成「没填」而去补一个数
            <Tag color='grey' shape='circle'>
              {t('不赠送')}
            </Tag>
          ),
      },
      { title: t('备注'), dataIndex: 'remark', render: (v) => v || '-' },
      {
        title: t('邀请链接'),
        dataIndex: 'aff_code',
        render: (affCode) =>
          affCode ? (
            <Button
              size='small'
              theme='borderless'
              onClick={async () => {
                if (await copy(affLinkOf(affCode))) {
                  showSuccess(t('邀请链接已复制'));
                } else {
                  showError(affLinkOf(affCode));
                }
              }}
            >
              {t('复制链接')}
            </Button>
          ) : (
            <Text type='tertiary' size='small'>
              {t('该用户尚未生成邀请码')}
            </Text>
          ),
      },
      {
        title: t('启用'),
        dataIndex: 'enabled',
        width: 90,
        render: (v, record) => (
          <Switch
            checked={v !== false}
            disabled={disabled || saving}
            onChange={(checked) =>
              persist(
                rules.map((r) =>
                  r.inviter_id === record.inviter_id
                    ? { ...r, enabled: checked }
                    : r,
                ),
              )
            }
          />
        ),
      },
      {
        title: t('操作'),
        width: 80,
        render: (_, record) => (
          <Popconfirm
            title={t('确定删除该渠道奖励？')}
            content={t('删除后通过该渠道注册的新用户将回到默认注册赠分')}
            onConfirm={() =>
              persist(rules.filter((r) => r.inviter_id !== record.inviter_id))
            }
          >
            <Button
              size='small'
              type='danger'
              theme='borderless'
              icon={<IconDelete />}
              disabled={disabled || saving}
            />
          </Popconfirm>
        ),
      },
    ],
    [rules, persist, disabled, saving, t],
  );

  return (
    <Card
      title={t('渠道积分奖励')}
      headerExtraContent={
        <Button
          icon={<IconPlus />}
          theme='outline'
          disabled={disabled}
          onClick={() => {
            setDraft(null);
            setUserOptions([]);
            setModalVisible(true);
          }}
        >
          {t('添加渠道')}
        </Button>
      }
      style={{ marginBottom: 15 }}
    >
      <Banner
        type='info'
        closeIcon={null}
        description={
          <div className='text-xs leading-6'>
            <div>
              {t(
                '用这里配置的渠道商邀请链接注册的新用户，注册赠分按本表的数值发放，覆盖上方的「新用户注册赠送积分」。其他渠道注册的用户不受影响。',
              )}
            </div>
            <div>
              {t(
                '对外宣传口径：注册即到账、可直接抵扣用量，不需要实名。填 0 表示该渠道不送积分。',
              )}
            </div>
            <div>
              {t(
                '只认直接邀请人：A 邀请 B、B 邀请 C 时，C 按 B 的配置发放。配置只影响之后注册的用户，已注册的不补发。',
              )}
            </div>
          </div>
        }
        style={{ marginBottom: 16 }}
      />

      {disabled && (
        <Banner
          type='warning'
          closeIcon={null}
          description={t('积分系统总开关未启用，本表配置不会生效。')}
          style={{ marginBottom: 16 }}
        />
      )}

      <Table
        columns={columns}
        dataSource={rules}
        rowKey='inviter_id'
        size='small'
        pagination={false}
        loading={saving}
        empty={
          <Empty
            description={t('尚未配置渠道奖励，所有新用户按默认注册赠分发放')}
          />
        }
      />

      <Modal
        title={t('添加渠道奖励')}
        visible={modalVisible}
        onCancel={() => setModalVisible(false)}
        onOk={addRule}
        okText={t('保存')}
        confirmLoading={saving}
      >
        <Form>
          <Form.Select
            field='inviter_id'
            label={t('渠道商用户')}
            placeholder={t('输入用户名 / 邮箱 / ID 搜索')}
            filter
            remote
            loading={searching}
            optionList={userOptions}
            onSearch={searchUsers}
            onChange={(v) => {
              const picked = userOptions.find((o) => o.value === v);
              setDraft((prev) => ({
                ...prev,
                inviter_id: v,
                username: picked?.username || '',
                // 冗余存 aff_code 只为列表直接出链接，判定一律以 inviter_id 为准
                aff_code: picked?.affCode || '',
              }));
            }}
            style={{ width: '100%' }}
            extraText={t('已配置过的用户不会出现在搜索结果中')}
          />
          <Form.InputNumber
            field='points'
            label={t('奖励积分')}
            placeholder={t('输入积分数')}
            min={0}
            precision={0}
            step={1}
            initValue={0}
            onChange={(v) =>
              setDraft((prev) => ({
                ...prev,
                points: Math.floor(Number(v) || 0),
              }))
            }
            style={{ width: '100%' }}
            extraText={t('0 = 该渠道不赠送积分（不会回落到默认值）')}
          />
          <Form.Input
            field='remark'
            label={t('备注')}
            placeholder={t('渠道名 / 合作方，会写进用户的赠分日志')}
            onChange={(v) => setDraft((prev) => ({ ...prev, remark: v }))}
            style={{ width: '100%' }}
          />
        </Form>
      </Modal>
    </Card>
  );
}
