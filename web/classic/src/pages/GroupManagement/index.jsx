import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  Button,
  Card,
  Col,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Tabs,
  Typography,
} from '@douyinfe/semi-ui';
import { IconRefresh, IconSave } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import {
  API,
  compareObjects,
  showError,
  showSuccess,
  showWarning,
  toBoolean,
} from '../../helpers';

import MismatchBanner from './components/MismatchBanner';
import GroupTable from './components/GroupTable';
import AutoGroupList from './components/AutoGroupList';
import GroupGroupRatioRules from './components/GroupGroupRatioRules';
import GroupSpecialUsableRules from './components/GroupSpecialUsableRules';
import ModelRatioEditor from './components/ModelRatioEditor';
import RatioSimulator from './components/RatioSimulator';
import GroupExtraSettings from './components/GroupExtraSettings';

const { Text, Title } = Typography;

/**
 * 分组管理。
 *
 * 改造前分组的配置项散在五个页面：分组倍率四件套 + 自动分组在「分组与模型定价设置」，
 * 充值倍率在支付设置，速率限制在速率限制设置，积分白名单在运营设置。新建一个分组
 * 要在这几页之间来回跳，也没有任何地方能一眼看全「这个分组到底是什么配置」。
 *
 * 这里只搬 UI，不搬存储：每个 Section 仍然读写它原本的 option key，保存依旧走
 * PUT /api/option/。设计见 docs/group-management-redesign.md §7。
 */

const OPTION_KEYS = [
  'GroupRatio',
  'UserUsableGroups',
  'GroupDescription',
  'GroupEnabled',
  'GroupGroupRatio',
  'GroupModelRatio',
  'UserGroupModelRatio',
  'group_ratio_setting.group_special_usable_group',
  'AutoGroups',
  'DefaultUseAutoGroup',
  'TopupGroupRatio',
  'ModelRequestRateLimitGroup',
  'points_setting.enabled_groups',
  'points_setting.enabled',
];

const BOOLEAN_KEYS = ['DefaultUseAutoGroup', 'points_setting.enabled'];

// 只读不写：积分总开关归「运营设置 → 积分设置」管，这里只用它决定白名单是否生效。
// 放进保存队列的话，两个页面就都能改同一个 key，谁后保存谁生效。
const READ_ONLY_KEYS = new Set(['points_setting.enabled']);

function parseJSONSafe(str, fallback) {
  if (!str || !str.trim()) return fallback;
  try {
    return JSON.parse(str);
  } catch {
    return fallback;
  }
}

export default function GroupManagementPage() {
  const { t } = useTranslation();

  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [inputs, setInputs] = useState({});
  const [originInputs, setOriginInputs] = useState({});
  const [overview, setOverview] = useState({ groups: [], unconfigured: [] });
  const [activeGroup, setActiveGroup] = useState('');
  const [activeTier, setActiveTier] = useState('');
  const [activeTab, setActiveTab] = useState('groups');
  const [seedNames, setSeedNames] = useState(null);
  const dataVersionRef = useRef(0);

  const loadOptions = useCallback(async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (!success) {
      showError(message);
      return;
    }
    const next = {};
    data.forEach((item) => {
      if (!OPTION_KEYS.includes(item.key)) return;
      if (BOOLEAN_KEYS.includes(item.key)) {
        next[item.key] = toBoolean(item.value);
        return;
      }
      let value = item.value;
      if (value?.startsWith('{') || value?.startsWith('[')) {
        try {
          value = JSON.stringify(JSON.parse(value), null, 2);
        } catch {
          // 后端返回的不是合法 JSON 时原样展示，别把用户手写的内容吃掉
        }
      }
      next[item.key] = value;
    });
    setInputs(next);
    setOriginInputs(structuredClone(next));
    dataVersionRef.current += 1;
  }, []);

  const loadOverview = useCallback(async () => {
    try {
      const res = await API.get('/api/group/overview');
      if (res.data?.success) {
        setOverview(res.data.data || { groups: [], unconfigured: [] });
      }
    } catch {
      // 健康数据拿不到不该挡住配置本身，表格里会退化成「未保存」
    }
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      await Promise.all([loadOptions(), loadOverview()]);
    } finally {
      setLoading(false);
    }
  }, [loadOptions, loadOverview]);

  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const groupNames = useMemo(
    () => Object.keys(parseJSONSafe(inputs.GroupRatio, {})),
    [inputs.GroupRatio],
  );

  // 用户档来源有两处：已配过折扣的档，以及现有分组名（现网两者高度重叠）。
  // 不能只取 GroupRatio ——谈判档位按设计 §6.4 不进 GroupRatio，那是路由维度，
  // 客户越多列表越乱。所以下拉允许直接新建。
  const tierNames = useMemo(() => {
    const configured = Object.keys(
      parseJSONSafe(inputs.UserGroupModelRatio, {}),
    );
    const merged = new Set([
      ...configured,
      ...Object.keys(parseJSONSafe(inputs.GroupRatio, {})),
    ]);
    return Array.from(merged).sort();
  }, [inputs.UserGroupModelRatio, inputs.GroupRatio]);

  const healthMap = useMemo(() => {
    const map = {};
    (overview.groups || []).forEach((g) => {
      map[g.name] = g;
    });
    return map;
  }, [overview]);

  // 首个分组作为折扣编辑器的默认选中项；分组被删掉后要跟着让位
  useEffect(() => {
    if (groupNames.length === 0) {
      if (activeGroup) setActiveGroup('');
      return;
    }
    if (!activeGroup || !groupNames.includes(activeGroup)) {
      setActiveGroup(groupNames[0]);
    }
  }, [groupNames, activeGroup]);

  // 档位折扣的默认选中项只取**已配过折扣的**档。
  //
  // 不能照搬上面 activeGroup 取 tierNames[0]：tierNames 把全部分组名也并了进来
  // （free、bailian 这些基本都没配档位折扣），选中一个没配过的档，规则表是空的，
  // 与「配置丢了」在视觉上无法区分。
  //
  // 这正是没有默认选中时的症状：配好保存成功、离开页面再回来，下拉框归零、表格
  // 空白——看起来和从未保存过一模一样，人的第一反应是重配一遍。
  const configuredTiers = useMemo(
    () => Object.keys(parseJSONSafe(inputs.UserGroupModelRatio, {})).sort(),
    [inputs.UserGroupModelRatio],
  );

  useEffect(() => {
    // 只补空白，不抢已有选择：下拉允许直接输入新档名，那一刻 activeTier 还不在
    // 任何列表里，若在这里重置就会把正在新建的档打断。
    if (activeTier || configuredTiers.length === 0) return;
    setActiveTier(configuredTiers[0]);
  }, [configuredTiers, activeTier]);

  const setField = useCallback((key, value) => {
    setInputs((prev) => ({ ...prev, [key]: value }));
  }, []);

  // 必须接住 serializeGroupTable 返回的**全部** key。少解构一个的表现是：
  // 勾选框能动（rows 本地 state 变了）、保存提示成功、刷新回滚——因为
  // inputs 里那个字段从未更新，compareObjects 检测不到变化，PUT 里就没有它。
  const handleGroupTableChange = useCallback(
    ({ GroupRatio, UserUsableGroups, GroupDescription, GroupEnabled }) => {
      setInputs((prev) => ({
        ...prev,
        GroupRatio,
        UserUsableGroups,
        GroupDescription,
        GroupEnabled,
      }));
    },
    [],
  );

  const onSubmit = useCallback(async () => {
    const updateArray = compareObjects(inputs, originInputs).filter(
      (item) => !READ_ONLY_KEYS.has(item.key),
    );
    if (!updateArray.length) {
      showWarning(t('你似乎并没有修改什么'));
      return;
    }
    setSaving(true);
    try {
      const results = await Promise.all(
        updateArray.map((item) =>
          API.put('/api/option/', {
            key: item.key,
            value:
              typeof inputs[item.key] === 'boolean'
                ? String(inputs[item.key])
                : inputs[item.key],
          }),
        ),
      );
      const failed = results.find((r) => !r?.data?.success);
      if (failed) {
        showError(failed.data?.message || t('保存失败'));
        return;
      }
      showSuccess(t('保存成功'));
      await refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  }, [inputs, originInputs, refresh, t]);

  const jumpToRules = useCallback((name) => {
    setActiveGroup(name);
    setActiveTab('model_ratio');
  }, []);

  const dv = dataVersionRef.current;
  const activeGroupRatio = useMemo(
    () => parseJSONSafe(inputs.GroupRatio, {})[activeGroup] ?? 1,
    [inputs.GroupRatio, activeGroup],
  );

  return (
    <div className='mt-[60px] px-2'>
      <Spin spinning={loading}>
        <Card
          className='!rounded-2xl'
          title={
            <div>
              <Title heading={4}>{t('分组管理')}</Title>
              <Text type='tertiary' size='small'>
                {t(
                  '分组决定计费倍率与模型访问范围。新建分组后记得去渠道管理把渠道挂到该分组上，否则用户选中会报「无可用渠道」。',
                )}
              </Text>
            </div>
          }
          headerExtraContent={
            <Space>
              <Button
                icon={<IconRefresh />}
                theme='borderless'
                onClick={refresh}
              >
                {t('刷新')}
              </Button>
              <Button
                icon={<IconSave />}
                theme='solid'
                loading={saving}
                onClick={onSubmit}
              >
                {t('保存')}
              </Button>
            </Space>
          }
        >
          <MismatchBanner
            unconfigured={overview.unconfigured}
            onCreateMissing={setSeedNames}
          />

          <Tabs type='line' activeKey={activeTab} onChange={setActiveTab}>
            <Tabs.TabPane tab={t('分组')} itemKey='groups'>
              <div className='pt-3'>
                <Text type='tertiary' size='small' className='mb-3 block'>
                  {t(
                    '倍率是计费乘数；勾选「用户可选」后该分组会出现在用户创建令牌的下拉里。未勾选的分组只能由管理员分配给用户。',
                  )}
                </Text>
                <GroupTable
                  key={`gt_${dv}`}
                  groupRatio={inputs.GroupRatio}
                  userUsableGroups={inputs.UserUsableGroups}
                  groupDescription={inputs.GroupDescription}
                  groupEnabled={inputs.GroupEnabled}
                  health={healthMap}
                  seedNames={seedNames}
                  onSelectGroup={jumpToRules}
                  onChange={handleGroupTableChange}
                />
              </div>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('模型折扣')} itemKey='model_ratio'>
              <div className='pt-3'>
                <Row gutter={12} className='mb-3'>
                  <Col xs={24} sm={8}>
                    <Text type='tertiary' size='small' className='mb-1 block'>
                      {t('配置哪个分组')}
                    </Text>
                    <Select
                      key={groupNames.length ? 'ready' : 'empty'}
                      style={{ width: '100%' }}
                      value={activeGroup || null}
                      optionList={groupNames.map((g) => ({
                        label: g,
                        value: g,
                      }))}
                      onChange={setActiveGroup}
                      filter
                      placeholder={t('选择分组')}
                    />
                  </Col>
                </Row>
                <ModelRatioEditor
                  key={`mre_${dv}_${activeGroup}`}
                  group={activeGroup}
                  groupRatio={activeGroupRatio}
                  value={inputs.GroupModelRatio}
                  staleRules={healthMap[activeGroup]?.stale_rules || []}
                  onChange={(v) => setField('GroupModelRatio', v)}
                />
              </div>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('档位折扣')} itemKey='user_tier'>
              <div className='pt-3'>
                <Row gutter={12} className='mb-3'>
                  <Col xs={24} sm={8}>
                    <Text type='tertiary' size='small' className='mb-1 block'>
                      {t('配置哪个用户档')}
                    </Text>
                    {/*
                      key 是必须的：Semi Select 在 optionList 从空变非空后不更新
                      内部选项，展开永远是「暂无数据」。而这里的时序恰好如此——
                      首次渲染时 inputs 还没加载，tierNames 是空数组，选项到达时
                      Select 已经挂载完了。

                      只在空/非空之间切换 key（而不是 tierNames.join()），
                      这样新建档位时不会重建组件、打断正在输入的档名。
                    */}
                    <Select
                      key={tierNames.length ? 'ready' : 'empty'}
                      style={{ width: '100%' }}
                      value={activeTier || null}
                      optionList={tierNames.map((g) => ({
                        label: g,
                        value: g,
                      }))}
                      onChange={setActiveTier}
                      filter
                      allowCreate
                      placeholder={t('选择或输入用户档（如客户名）')}
                    />
                  </Col>
                </Row>
                <ModelRatioEditor
                  key={`ugmr_${dv}_${activeTier}`}
                  group={activeTier}
                  groupRatio={1}
                  value={inputs.UserGroupModelRatio}
                  onChange={(v) => setField('UserGroupModelRatio', v)}
                  modelsEndpoint='/api/group/models'
                  allowOverride={false}
                  texts={{
                    emptyHint: t('请先选择或输入一个用户档'),
                    banner: (
                      <>
                        <div>
                          {t(
                            '按「用户档 × 模型」打折，与用户走哪条供应链无关——同一个用户用哪个令牌都是这个折扣。',
                          )}
                        </div>
                        <div>
                          {t(
                            '「*」是兜底规则，匹配所有模型；具体模型名与前缀通配优先级更高。',
                          )}
                        </div>
                        <div>
                          {t(
                            '全线折扣务必用「*」而不是逐个勾选：逐个勾会漏掉模型名的大小写变体，新上线的模型也不会自动纳入。',
                          )}
                        </div>
                      </>
                    ),
                  }}
                />
              </div>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('自动分组')} itemKey='auto'>
              <div className='pt-3'>
                <Text type='tertiary' size='small' className='mb-3 block'>
                  {t(
                    '令牌分组设为 auto 时，按以下顺序依次尝试可用分组，排在前面的优先级更高。',
                  )}
                </Text>
                <div className='mb-4 flex items-center gap-2'>
                  <Switch
                    checked={!!inputs.DefaultUseAutoGroup}
                    onChange={(v) => setField('DefaultUseAutoGroup', v)}
                  />
                  <Text>{t('创建令牌时默认选择 auto 分组')}</Text>
                </div>
                <AutoGroupList
                  key={`ag_${dv}`}
                  value={inputs.AutoGroups}
                  groupNames={groupNames}
                  onChange={(v) => setField('AutoGroups', v)}
                />
              </div>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('跨分组规则')} itemKey='cross'>
              <div className='pt-3'>
                <Title heading={6} className='mb-1'>
                  {t('身份折扣')}
                </Title>
                <Text type='tertiary' size='small' className='mb-3 block'>
                  {t(
                    '某个分组的用户使用另一个分组的令牌时，用这里的倍率覆盖分组基础倍率。例如 vip 用户使用 premium 令牌时按 0.7 计费。',
                  )}
                </Text>
                <GroupGroupRatioRules
                  key={`ggr_${dv}`}
                  value={inputs.GroupGroupRatio}
                  groupNames={groupNames}
                  onChange={(v) => setField('GroupGroupRatio', v)}
                />

                <Title heading={6} className='mb-1 mt-6'>
                  {t('可用分组增减')}
                </Title>
                <Text type='tertiary' size='small' className='mb-3 block'>
                  {t(
                    '为特定用户分组增减可用分组。「添加」让该分组的用户额外能选某个分组，「移除」收回默认可选的分组。',
                  )}
                </Text>
                <GroupSpecialUsableRules
                  key={`gsu_${dv}`}
                  value={
                    inputs['group_ratio_setting.group_special_usable_group']
                  }
                  groupNames={groupNames}
                  onChange={(v) =>
                    setField(
                      'group_ratio_setting.group_special_usable_group',
                      v,
                    )
                  }
                />
              </div>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('充值 · 限流 · 积分')} itemKey='extra'>
              <div className='pt-3'>
                <GroupExtraSettings
                  key={`ge_${dv}`}
                  inputs={inputs}
                  groupNames={groupNames}
                  onChange={setField}
                />
              </div>
            </Tabs.TabPane>
          </Tabs>
        </Card>

        <div className='mt-4'>
          <RatioSimulator groupNames={groupNames} />
        </div>
      </Spin>
    </div>
  );
}
