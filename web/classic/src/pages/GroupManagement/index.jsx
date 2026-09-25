import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  Banner,
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
import DanglingBanner from './components/DanglingBanner';
import GroupTable from './components/GroupTable';
import AutoGroupList from './components/AutoGroupList';
import GroupGroupRatioRules from './components/GroupGroupRatioRules';
import GroupSpecialUsableRules from './components/GroupSpecialUsableRules';
import ModelRatioEditor from './components/ModelRatioEditor';
import TimeWindowEditor from './components/TimeWindowEditor';
import RatioSimulator from './components/RatioSimulator';
import GroupExtraSettings from './components/GroupExtraSettings';
import TierUsableMatrix from './components/TierUsableMatrix';

const { Text, Title } = Typography;

/**
 * 分组管理。
 *
 * 「分组」在系统里同时承担两个正交的概念，页面按这两个轴组织，而不是按 option key：
 *
 *   线路   —— token.group / channel.group。决定请求走哪些渠道、成本多少。
 *            GroupRatio、GroupModelRatio、GroupTimeRatio 按它索引。
 *   用户档 —— user.group。决定这批用户打几折、能用哪些线路。
 *            UserGroupModelRatio、group_special_usable_group、GroupGroupRatio、
 *            TopupGroupRatio、ModelRequestRateLimitGroup、积分白名单按它索引。
 *
 * 两者共用同一个名字空间（一个用户档可以有一条同名线路），所以底层仍是同一份
 * GroupRatio；页面只是把「问的是哪个轴的问题」分开。
 *
 * 只搬 UI，不搬存储：每个 Section 仍然读写它原本的 option key，保存依旧走
 * PUT /api/option/。概念说明见 docs/group-concepts.md。
 */

const OPTION_KEYS = [
  'GroupRatio',
  'UserUsableGroups',
  'GroupDescription',
  'GroupEnabled',
  'GroupGroupRatio',
  'GroupModelRatio',
  'UserGroupModelRatio',
  'GroupTimeRatio',
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

const EMPTY_OVERVIEW = {
  groups: [],
  unconfigured: [],
  usable_matrix: {},
  dangling: [],
};

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
  const [overview, setOverview] = useState(EMPTY_OVERVIEW);
  // 每个时段模板被多少条规则引用（跨全部线路）。删模板前要看得见影响面——
  // 直接删掉一个还被引用的模板，那些规则会变成悬空引用，保存时后端整份拒绝，
  // 而错误信息指向的是规则不是模板。
  const timeWindowUsage = useMemo(() => {
    const usage = {};
    try {
      const rules = JSON.parse(inputs.GroupTimeRatio || '{}')?.rules || {};
      Object.values(rules).forEach((groupRules) => {
        Object.values(groupRules || {}).forEach((list) => {
          (list || []).forEach((r) => {
            if (!r?.window) return;
            usage[r.window] = (usage[r.window] || 0) + 1;
          });
        });
      });
    } catch (e) {
      // 手改坏的 JSON：引用数显示 0，保存时后端会给出确切错误
    }
    return usage;
  }, [inputs.GroupTimeRatio]);

  const [activeGroup, setActiveGroup] = useState('');
  const [activeTier, setActiveTier] = useState('');
  const [activeTab, setActiveTab] = useState('lines');
  const [lineSubTab, setLineSubTab] = useState('list');
  const [tierSubTab, setTierSubTab] = useState('usable');
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
        setOverview({ ...EMPTY_OVERVIEW, ...(res.data.data || {}) });
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

  // 线路名 = GroupRatio 的 key。auto 是伪分组，不是线路，GroupTable 保存时不会写进来。
  const groupNames = useMemo(
    () => Object.keys(parseJSONSafe(inputs.GroupRatio, {})),
    [inputs.GroupRatio],
  );

  // 用户档来源有两处：已配过折扣的档，以及现有线路名（一个用户档常有一条同名线路）。
  // 不能只取 GroupRatio ——谈判档位按设计不进 GroupRatio，那是路由维度，
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

  // 首条线路作为模型定价编辑器的默认选中项；线路被删掉后要跟着让位
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
  // 不能照搬上面 activeGroup 取 tierNames[0]：tierNames 把全部线路名也并了进来
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
    setActiveTab('lines');
    setLineSubTab('pricing');
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
                  '线路决定请求走哪些渠道、成本多少；用户档决定这批用户打几折、能用哪些线路。新建线路后记得去渠道管理把渠道挂上去，否则用户选中会报「无可用渠道」。',
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
          {/*
            公式条常驻页面顶部。五层解析、三种叠加语义是运营看不懂的根源，
            所有 Tab 的说明都对齐到这一句，不再各说各的。
          */}
          <Banner
            type='info'
            closeIcon={null}
            className='mb-3'
            description={
              <div className='text-sm leading-6'>
                <Text strong>
                  {t(
                    '最终倍率 = 线路价（基础倍率 → 模型定价 → 时段价） × 用户档折扣',
                  )}
                </Text>
                <div>
                  <Text type='tertiary' size='small'>
                    {t(
                      '线路价按令牌所选线路计算，用户档折扣按用户所属档计算，两者相乘。「按线路覆盖」是历史配置项，会替换线路基础倍率，见用户档 → 高级。',
                    )}
                  </Text>
                </div>
              </div>
            }
          />

          <MismatchBanner
            unconfigured={overview.unconfigured}
            onCreateMissing={setSeedNames}
          />
          <DanglingBanner dangling={overview.dangling} />

          <Tabs type='line' activeKey={activeTab} onChange={setActiveTab}>
            <Tabs.TabPane tab={t('线路')} itemKey='lines'>
              <Tabs
                type='button'
                size='small'
                activeKey={lineSubTab}
                onChange={setLineSubTab}
                className='pt-2'
              >
                <Tabs.TabPane tab={t('线路列表')} itemKey='list'>
                  <div className='pt-3'>
                    <Text type='tertiary' size='small' className='mb-3 block'>
                      {t(
                        '基础倍率是该线路的计费乘数；勾选「用户可选」后所有用户创建令牌时都能选到它。未勾选的线路只有属于同名用户档的用户、或在「用户档 → 可用线路」里被添加的用户能用。',
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

                <Tabs.TabPane tab={t('模型定价与时段价')} itemKey='pricing'>
                  <div className='pt-3'>
                    <Row gutter={12} className='mb-3'>
                      <Col xs={24} sm={8}>
                        <Text
                          type='tertiary'
                          size='small'
                          className='mb-1 block'
                        >
                          {t('配置哪条线路')}
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
                          placeholder={t('选择线路')}
                        />
                      </Col>
                    </Row>
                    {/*
                      时段模板放在规则表上方而不是另开一个 Tab：规则要引用模板，
                      分成两个 Tab 会让人配规则时找不到模板、或者建完模板忘了回来配规则。
                    */}
                    <TimeWindowEditor
                      value={inputs.GroupTimeRatio}
                      onChange={(v) => setField('GroupTimeRatio', v)}
                      usage={timeWindowUsage}
                    />
                    <ModelRatioEditor
                      key={`mre_${dv}_${activeGroup}`}
                      group={activeGroup}
                      groupRatio={activeGroupRatio}
                      value={inputs.GroupModelRatio}
                      staleRules={healthMap[activeGroup]?.stale_rules || []}
                      onChange={(v) => setField('GroupModelRatio', v)}
                      syncTargets={groupNames}
                      timeValue={inputs.GroupTimeRatio}
                      onTimeChange={(v) => setField('GroupTimeRatio', v)}
                    />
                  </div>
                </Tabs.TabPane>
              </Tabs>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('用户档')} itemKey='tiers'>
              <Tabs
                type='button'
                size='small'
                activeKey={tierSubTab}
                onChange={setTierSubTab}
                className='pt-2'
              >
                <Tabs.TabPane tab={t('可用线路')} itemKey='usable'>
                  <div className='pt-3'>
                    <TierUsableMatrix matrix={overview.usable_matrix} />

                    <Title heading={6} className='mb-1 mt-6'>
                      {t('调整规则')}
                    </Title>
                    <Text type='tertiary' size='small' className='mb-3 block'>
                      {t(
                        '默认每个用户档都能用所有「用户可选」线路，以及与自己同名的线路。这里按用户档增减：「添加」让该档用户额外能选某条线路，「移除」收回一条默认可选的线路。保存后上表会更新。',
                      )}
                    </Text>
                    <GroupSpecialUsableRules
                      key={`gsu_${dv}`}
                      value={
                        inputs['group_ratio_setting.group_special_usable_group']
                      }
                      groupNames={groupNames}
                      tierNames={tierNames}
                      onChange={(v) =>
                        setField(
                          'group_ratio_setting.group_special_usable_group',
                          v,
                        )
                      }
                    />
                  </div>
                </Tabs.TabPane>

                <Tabs.TabPane tab={t('档位折扣')} itemKey='discount'>
                  <div className='pt-3'>
                    <Row gutter={12} className='mb-3'>
                      <Col xs={24} sm={8}>
                        <Text
                          type='tertiary'
                          size='small'
                          className='mb-1 block'
                        >
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
                      syncTargets={configuredTiers}
                      texts={{
                        emptyHint: t('请先选择或输入一个用户档'),
                        syncLabel: '同步到其他用户档',
                        banner: (
                          <>
                            <div>
                              {t(
                                '按「用户档 × 模型」打折，与用户走哪条线路无关——同一个用户用哪条线路的令牌都是这个折扣，乘在线路价之后。',
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

                <Tabs.TabPane tab={t('高级：按线路覆盖')} itemKey='advanced'>
                  <div className='pt-3'>
                    {/*
                      历史配置项 GroupGroupRatio（曾叫「分组特殊倍率」「身份折扣」）。
                      语义是「某用户档使用某条线路时，直接替换该线路的基础倍率」——
                      是覆盖不是打折，且会被「定价 =」规则吃掉。它与档位折扣同为
                      售价侧，但按 (用户档, 线路) 索引，档位折扣按 (用户档, 模型) 索引，
                      两者不能无损互转，所以这里不做自动迁移，只降级入口并说明。
                    */}
                    <Banner
                      type='warning'
                      closeIcon={null}
                      className='mb-3'
                      description={
                        <div className='text-sm leading-6'>
                          <div>
                            {t(
                              '这是历史配置项「分组特殊倍率」：某用户档使用某条线路时，用这里的值直接替换该线路的基础倍率（是替换，不是打折）。之后仍会乘模型定价与用户档折扣，但会被「定价 =」规则覆盖。',
                            )}
                          </div>
                          <div>
                            {t(
                              '新配置请优先用「档位折扣」的「*」规则（对所有线路统一打折）。只有同一用户档在不同线路需要不同价时才用这里。',
                            )}
                          </div>
                        </div>
                      }
                    />
                    <GroupGroupRatioRules
                      key={`ggr_${dv}`}
                      value={inputs.GroupGroupRatio}
                      groupNames={groupNames}
                      tierNames={tierNames}
                      onChange={(v) => setField('GroupGroupRatio', v)}
                    />
                  </div>
                </Tabs.TabPane>
              </Tabs>
            </Tabs.TabPane>

            <Tabs.TabPane tab={t('自动分组')} itemKey='auto'>
              <div className='pt-3'>
                <Text type='tertiary' size='small' className='mb-3 block'>
                  {t(
                    '令牌线路设为 auto 时，按以下顺序依次尝试该用户可用的线路，排在前面的优先。不在用户可用线路里的会被跳过。',
                  )}
                </Text>
                <div className='mb-4 flex items-center gap-2'>
                  <Switch
                    checked={!!inputs.DefaultUseAutoGroup}
                    onChange={(v) => setField('DefaultUseAutoGroup', v)}
                  />
                  <Text>{t('创建令牌时默认选择 auto')}</Text>
                </div>
                <AutoGroupList
                  key={`ag_${dv}`}
                  value={inputs.AutoGroups}
                  groupNames={groupNames}
                  onChange={(v) => setField('AutoGroups', v)}
                />
              </div>
            </Tabs.TabPane>
          </Tabs>
        </Card>

        <div className='mt-4'>
          <RatioSimulator groupNames={groupNames} tierNames={tierNames} />
        </div>
      </Spin>
    </div>
  );
}
