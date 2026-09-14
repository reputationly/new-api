import React, { useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  Collapse,
  Input,
  Modal,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../helpers';
import {
  MODERATION_CATEGORY_ACTIONS,
  MODERATION_CATEGORY_DEFAULTS,
  MODERATION_GROUP_MODES,
  MODERATION_POLICY_CATEGORIES,
  MODERATION_STRICTNESS,
  moderationAbsentCategoryAction,
  moderationCategoryLabel,
  moderationDialectCovers,
  moderationDialectDefault,
  moderationStrictnessApplies,
} from '../../../constants/moderation.constants';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

// 审核策略。与「内容审核」卡片分开：那张管「审不审、用什么审」，这张管「怎么判」。
//
// 三个键都在这里编辑，它们互相引用，必须一起看：
//   moderation.policies        策略列表（严格度 + 九类处置）
//   moderation.default_policy  未绑定的分组用哪条
//   moderation.group_policies  分组 → {模式, 策略}
//
// 后端对三者都做了交叉校验（删掉被引用的策略、绑定不存在的策略、默认策略指向空
// 都会被拒），因为这几种错配的后果全是**静默回退**：ResolvePolicy 找不到就回退
// 默认策略、再回退第一条，某个分组的判定规则悄悄换了一套而界面上什么都看不出来。

const DEFAULT_POLICY = () => ({
  name: '',
  strictness: 'standard',
  // 新策略的默认值取 MODERATION_CATEGORY_DEFAULTS，与后端内置的「标准」策略和
  // system_setting.defaultCategoryActions 同源。
  //
  // 不在这里重写一遍分类逻辑：上一版是一串写死类别名的三元表达式，
  // 加一个类别就得同时改这里、后端内置策略、后端默认表三处，
  // 而漏改的表现是新建的策略里那一类静默拿到 ignore。
  categories: MODERATION_POLICY_CATEGORIES.reduce((acc, c) => {
    acc[c.value] = MODERATION_CATEGORY_DEFAULTS[c.value] || 'block';
    return acc;
  }, {}),
});

/**
 * 当前启用节点用的判定协议。
 *
 * 从 moderation.endpoints 就地推导，不另开接口：这张表要标注的是「按现在的配置，
 * 这一类真的会被判出来吗」，而唯一的依据就是启用了哪些节点。后端保证同模态下
 * 启用节点的协议唯一，所以取第一个即可。
 */
function activeDialect(rawEndpoints, modality) {
  const eps = parseJSON(rawEndpoints, []);
  if (!Array.isArray(eps)) return '';
  const hit = eps.find(
    (e) => e?.enabled && (e.modality || 'text') === modality,
  );
  if (!hit) return '';
  return hit.dialect || moderationDialectDefault(modality);
}

/**
 * 覆盖标注。四种状态都要能区分，尤其「当前不覆盖」——
 * 上一版只有「文本+图片 / 仅文本」两档，而换了协议之后会出现真正一个模态都
 * 不覆盖的类别（如 Qwen3Guard 下的未成年人保护）。把它标成「仅文本」
 * 就是在说谎：运营配了拒绝，而那一行永远不会命中。
 */
function coverage(category, textDialect, imageDialect) {
  return {
    text: !!textDialect && moderationDialectCovers(textDialect, category),
    image: !!imageDialect && moderationDialectCovers(imageDialect, category),
  };
}

function coverageLabel(category, textDialect, imageDialect) {
  const c = coverage(category, textDialect, imageDialect);
  if (c.text && c.image) return '文本+图片';
  if (c.text) return '仅文本';
  if (c.image) return '仅图片';
  return '当前不覆盖';
}

function coverageColor(category, textDialect, imageDialect) {
  const c = coverage(category, textDialect, imageDialect);
  if (c.text && c.image) return 'blue';
  if (c.text || c.image) return 'grey';
  // 配了却完全不生效，是这张表上最需要被看见的状态。
  return 'red';
}

function coverageHint(category, textDialect, imageDialect) {
  const c = coverage(category, textDialect, imageDialect);
  if (c.text && c.image) {
    return '当前启用的文本与图片协议都能判出这一类，处置会真正生效。';
  }
  if (c.text) {
    return '只有文本能判出这一类。上传的图片和视频对它没有覆盖——连关键词层都扫不了图，没有任何兜底。';
  }
  if (c.image) {
    return '只有图片/视频能判出这一类，纯文本请求对它没有覆盖。';
  }
  return '当前启用的判定协议都产出不了这一类，这里配什么都不会生效。想覆盖它需要换用支持该类别的协议。';
}

function parseJSON(raw, fallback) {
  if (!raw) return fallback;
  try {
    const v = JSON.parse(raw);
    return v ?? fallback;
  } catch (e) {
    return fallback;
  }
}

export default function SettingsModerationPolicy(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [policies, setPolicies] = useState([]);
  const [defaultPolicy, setDefaultPolicy] = useState('');
  const [groupPolicies, setGroupPolicies] = useState({});
  const [allGroups, setAllGroups] = useState([]);
  // 近 7 天各类别的命中数。observe 期攒记录的全部意义就是回答「这一类拦了多少」，
  // 把数字放在开关旁边，调整才有依据——否则只能凭感觉调。
  const [catStats, setCatStats] = useState({});
  const mounted = useRef(false);

  // 当前生效的判定协议。决定这张表上每一类到底覆盖不覆盖、严格度是否有意义。
  const rawEndpoints = props.options?.['moderation.endpoints'];
  const textDialect = activeDialect(rawEndpoints, 'text');
  const imageDialect = activeDialect(rawEndpoints, 'image');
  // 严格度只对提供「有争议」中间档的协议有意义。两边都不支持时那个下拉框
  // 是个纯粹的摆设，必须在界面上说清楚——不然调了以为收紧了其实没有。
  const strictnessMatters =
    moderationStrictnessApplies(textDialect) ||
    moderationStrictnessApplies(imageDialect);

  useEffect(() => {
    const p = parseJSON(props.options?.['moderation.policies'], []);
    const g = parseJSON(props.options?.['moderation.group_policies'], {});
    setPolicies(Array.isArray(p) ? p : []);
    setGroupPolicies(g && typeof g === 'object' ? g : {});
    setDefaultPolicy(props.options?.['moderation.default_policy'] || '');
  }, [props.options]);

  useEffect(() => {
    if (mounted.current) return;
    mounted.current = true;
    // 列出**全部**分组而不是只列已配置的：按分组灰度的意义就是「谁跟谁不一样」，
    // 而看不见的分组正是最容易出事的地方。
    API.get('/api/group/')
      .then((res) => {
        if (res.data?.success) setAllGroups(res.data.data || []);
      })
      .catch(() => {});
    API.get('/api/moderation/category-stats')
      .then((res) => {
        if (res.data?.success) setCatStats(res.data.data || {});
      })
      .catch(() => {});
  }, []);

  // 分组行 = 当前可用分组 ∪ 已经配过绑定的分组。
  //
  // 后者不能漏：分组在「分组管理」里被删掉之后，group_policies 里的那条**仍然生效**
  // （任何还带着这个分组名的用户或令牌都会命中它），却从这个页面上消失了——
  // 既看不到也清不掉，而它还会一直挡着被它引用的那条策略不让删，
  // 报错里点名的又是一个管理员已经找不到的分组。
  const availableGroups = allGroups.map((g) =>
    typeof g === 'string' ? g : g.name || g.value,
  );
  const groupRows = [
    ...availableGroups.map((name) => ({ name, orphan: false })),
    ...Object.keys(groupPolicies)
      .filter((name) => !availableGroups.includes(name))
      .map((name) => ({ name, orphan: true })),
  ];

  function updatePolicy(idx, field, value) {
    // 改名要把引用一起带过去。
    //
    // 后端为「改名默认策略」专门做了原子接口，但那只保证**三者一致时**能存进去；
    // 界面这边如果只改 policies[idx].name，defaultPolicy 和各分组绑定还留着旧名，
    // 提交上去必然被拒——而此时默认策略下拉显示的正是那个旧名字，看起来完全正常，
    // 分组那一列的悬空绑定则显示为空白、看着像已解绑。两种都查不出原因。
    if (field === 'name') {
      const oldName = policies[idx]?.name;
      const newName = value;
      if (oldName && oldName !== newName) {
        if (defaultPolicy === oldName) setDefaultPolicy(newName);
        setGroupPolicies((prev) => {
          let touched = false;
          const next = {};
          for (const [g, gp] of Object.entries(prev)) {
            if (gp?.policy === oldName) {
              next[g] = { ...gp, policy: newName };
              touched = true;
            } else {
              next[g] = gp;
            }
          }
          return touched ? next : prev;
        });
      }
    }
    setPolicies((prev) =>
      prev.map((p, i) => (i === idx ? { ...p, [field]: value } : p)),
    );
  }

  function updateCategory(idx, cat, action) {
    // 兜底值必须与下面下拉框显示的那个**用同一个函数算**：
    // categories 里缺键时界面显示的是后端真会执行的动作，但原始值是 undefined。
    // 不兜底的话，把一个显示为「直接拒绝」的类别改成「仅记录」会**跳过**这条
    // 专门为放宽设计的确认——而存量策略、手写 JSON 都不要求九类齐全。
    //
    // 两处不一致同样有害：新增的五类缺键时显示的是各自默认值（如 cyber 显示
    // 「仅记录」），这里若写死 block，把它改成「不处理」会被当成 block→ignore
    // 而弹出一条根本不成立的放宽确认。
    const current =
      policies[idx]?.categories?.[cat] || moderationAbsentCategoryAction(cat);
    const apply = () =>
      setPolicies((prev) =>
        prev.map((p, i) =>
          i === idx
            ? { ...p, categories: { ...(p.categories || {}), [cat]: action } }
            : p,
        ),
      );

    // 放宽处置要二次确认。这类改动没有任何即时反馈——改错了要等到有人问
    // 「为什么没拦住」才发现，而那时已经漏了一整段时间。收紧则不必确认：
    // 误拦会立刻被用户投诉推到台前，是自暴露的。
    if (current === 'block' && action !== 'block') {
      Modal.confirm({
        title: t('确认放宽这个类别？'),
        content: (
          <div>
            <Text>
              {t('「')}
              {t(moderationCategoryLabel(cat))}
              {t('」将从「直接拒绝」改为「')}
              {t(
                MODERATION_CATEGORY_ACTIONS.find((a) => a.value === action)
                  ?.label || action,
              )}
              {t('」。')}
            </Text>
            <br />
            <Text type='warning'>
              {t(
                '这类改动没有即时反馈：保存后该类别的内容会直接放行，而你要等到有人问「为什么没拦住」才会发现改错了。',
              )}
            </Text>
          </div>
        ),
        onOk: apply,
      });
      return;
    }
    apply();
  }

  function addPolicy() {
    setPolicies((prev) => [...prev, DEFAULT_POLICY()]);
  }

  function removePolicy(idx) {
    const name = policies[idx]?.name;
    // 前端先拦一道：后端也会拒（ValidateModerationPoliciesJSON），但那时
    // 用户已经点了保存、还要自己回来找是哪条被引用了。
    if (name && name === defaultPolicy) {
      return showError(t('这是默认策略，请先把默认策略改成别的再删除'));
    }
    const boundBy = Object.entries(groupPolicies)
      .filter(([, gp]) => gp?.policy === name)
      .map(([g]) => g);
    if (boundBy.length) {
      return showError(
        t('分组 ') + boundBy.join('、') + t(' 还绑定着这条策略，请先解绑'),
      );
    }
    setPolicies((prev) => prev.filter((_, i) => i !== idx));
  }

  function updateGroup(group, field, value) {
    setGroupPolicies((prev) => {
      const next = { ...prev };
      const cur = { mode: '', policy: '', ...(next[group] || {}) };
      cur[field] = value;
      // 两项都是默认值时把整条删掉，别在配置里留一堆 {mode:"",policy:""} 的空壳
      if (!cur.mode && !cur.policy) delete next[group];
      else next[group] = cur;
      return next;
    });
  }

  async function onSubmit() {
    if (!policies.length) {
      return showError(t('至少要保留一条策略'));
    }
    for (const p of policies) {
      if (!(p.name || '').trim()) {
        return showError(t('每条策略都要有名称：分组是按名称绑定策略的'));
      }
    }
    if (!defaultPolicy) {
      return showError(t('请选择默认策略'));
    }

    // 三个键一次提交。
    //
    // 拆成三次 PUT 会让最自然的两种编辑做不成：改名默认策略时，先写 policies 会因
    // 旧的 default_policy 还指着旧名被拒，先写 default_policy 又会因新名还不存在被拒，
    // 两个方向都死锁；「先解绑再删策略」同理——前端按本地状态判断可以删，
    // 后端拿已存的 group_policies 一比还绑着，拒。
    setLoading(true);
    try {
      const res = await API.put('/api/moderation/policy-config', {
        policies,
        default_policy: defaultPolicy,
        group_policies: groupPolicies,
      });
      if (!res?.data?.success) {
        showError(res?.data?.message || t('保存失败'));
        return;
      }
      showSuccess(t('保存成功'));
      props.refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  }

  const categoryColumns = (idx) => [
    {
      title: t('风险类别'),
      dataIndex: 'value',
      width: 200,
      render: (v) => (
        <Space spacing={4}>
          <Text>{t(moderationCategoryLabel(v))}</Text>
          <Tooltip content={t(coverageHint(v, textDialect, imageDialect))}>
            <Tag
              color={coverageColor(v, textDialect, imageDialect)}
              shape='circle'
              size='small'
            >
              {t(coverageLabel(v, textDialect, imageDialect))}
            </Tag>
          </Tooltip>
        </Space>
      ),
    },
    {
      title: t('处置'),
      dataIndex: 'action',
      width: 140,
      render: (_, record) => (
        <Select
          // 未配置时显示的是**后端实际会用的那个动作**。
          //
          // 新增的五类（cyber / advice / minor / terror / vulgar）缺键时走各自的
          // 默认值，一律显示成「直接拒绝」是在说谎；而原来的九类缺键时后端仍然
          // 按「直接拒绝」处置（既有契约），显示成它们的开箱默认同样是说谎，
          // 方向还相反——会让人以为一条没配过的「严格」策略比实际宽松。
          value={
            policies[idx]?.categories?.[record.value] ||
            moderationAbsentCategoryAction(record.value)
          }
          style={{ width: '100%' }}
          onChange={(v) => updateCategory(idx, record.value, v)}
        >
          {MODERATION_CATEGORY_ACTIONS.map((a) => (
            <Select.Option key={a.value} value={a.value}>
              {t(a.label)}
            </Select.Option>
          ))}
        </Select>
      ),
    },
    {
      // 标题必须写明「全站」：这个数字不按策略、不按分组、也不看 enforced 过滤，
      // 而它渲染在每一条策略的面板里——不标的话会被读成「这条策略拦了 N 次」，
      // 而实际上其它分组在观察模式下的命中也混在里面。
      title: (
        <Tooltip
          content={t(
            '全站近 7 天该类别的命中次数（不分策略、不分分组、不分阶段——输入侧与产物侧的命中都算在内，也含仅观察模式下未真正拦截的判定）。用来看量级，不代表当前这条策略的效果。',
          )}
        >
          <span>{t('全站近 7 天命中')}</span>
        </Tooltip>
      ),
      dataIndex: 'stat',
      width: 140,
      render: (_, record) => {
        // 配成「不处理」的类别，判定结果是 pass，而 pass 记录按抽样率落库
        // （默认 1%）——统计出来的数字会低估两个数量级。显示一个低估百倍的数
        // 比不显示更糟：运营会读成「这一类很少见」，正好做出反向决策。
        const action =
          policies[idx]?.categories?.[record.value] ||
          moderationAbsentCategoryAction(record.value);
        if (action === 'ignore') {
          return (
            <Tooltip
              content={t(
                '配成「不处理」的类别判定为通过，而通过记录按抽样率落库（默认 1%），统计不出真实量级。想看这一类的真实命中，先改成「仅记录」观察一段时间。',
              )}
            >
              <Text type='tertiary'>{t('未统计')}</Text>
            </Tooltip>
          );
        }
        return catStats[record.value] ? (
          <Text>{catStats[record.value]}</Text>
        ) : (
          <Text type='tertiary'>0</Text>
        );
      },
    },
  ];

  return (
    <Card style={{ marginTop: 10 }}>
      <Typography.Title heading={6} style={{ marginBottom: 8 }}>
        {t('审核策略')}
      </Typography.Title>
      <Banner
        type='info'
        description={t(
          '策略决定「判成违规之后怎么办」，运行模式决定「拦不拦」——两者都满足才会真正拒绝请求。改动在下一个请求就生效，不需要重启。',
        )}
        style={{ marginBottom: 16 }}
      />
      {/*
        覆盖范围是随判定协议变的，所以这条提示不能写死某个模型。
        标红的「当前不覆盖」是最要紧的那一档：那一行配什么都不生效。
      */}
      <Banner
        type='warning'
        description={t(
          '每一类的覆盖范围由节点上选的判定协议决定，在下表的类别名后面标着。标「仅文本」的对上传的图片和视频不生效——连关键词层都扫不了图，没有任何兜底；标红的「当前不覆盖」表示现在启用的协议产出不了这一类，配了也不会生效。',
        )}
        style={{ marginBottom: 16 }}
      />
      {!textDialect && !imageDialect && (
        <Banner
          type='info'
          description={t(
            '还没有启用任何审核节点，因此下表暂时无法标注覆盖范围。请先在「内容审核」卡片里配置节点并选择判定协议。',
          )}
          style={{ marginBottom: 16 }}
        />
      )}
      {!strictnessMatters && (textDialect || imageDialect) && (
        <Banner
          type='warning'
          description={t(
            '当前启用的判定协议都是二分判定（只有安全/违规两档），下面的「严格度」对它们毫无影响。松紧请用类别处置表调。',
          )}
          style={{ marginBottom: 16 }}
        />
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} md={8}>
          <div style={{ marginBottom: 4 }}>{t('默认策略')}</div>
          <Select
            value={defaultPolicy}
            style={{ width: '100%' }}
            placeholder={t('选择一条策略')}
            onChange={setDefaultPolicy}
          >
            {policies
              .filter((p) => (p.name || '').trim())
              .map((p) => (
                <Select.Option key={p.name} value={p.name}>
                  {p.name}
                </Select.Option>
              ))}
          </Select>
          <div style={{ marginTop: 4 }}>
            <Text type='tertiary' size='small'>
              {t('没有单独绑定策略的分组都用它')}
            </Text>
          </div>
        </Col>
      </Row>

      <Collapse accordion>
        {policies.map((p, idx) => (
          <Collapse.Panel
            key={idx}
            itemKey={String(idx)}
            header={
              <Space spacing={8}>
                <Text strong>{p.name || t('（未命名策略）')}</Text>
                <Tag shape='circle' size='small'>
                  {t(
                    MODERATION_STRICTNESS.find(
                      (s) => s.value === (p.strictness || 'standard'),
                    )?.label || p.strictness,
                  )}
                </Tag>
                {p.name === defaultPolicy && (
                  <Tag color='green' shape='circle' size='small'>
                    {t('默认')}
                  </Tag>
                )}
              </Space>
            }
          >
            <Row gutter={16} style={{ marginBottom: 12 }}>
              <Col xs={24} sm={12} md={8}>
                <div style={{ marginBottom: 4 }}>{t('策略名称')}</div>
                <Input
                  value={p.name}
                  placeholder={t('标准 / 严格 / 宽松')}
                  onChange={(v) => updatePolicy(idx, 'name', v)}
                />
              </Col>
              <Col xs={24} sm={12} md={10}>
                <div style={{ marginBottom: 4 }}>{t('判定严格度')}</div>
                <Select
                  value={p.strictness || 'standard'}
                  style={{ width: '100%' }}
                  onChange={(v) => updatePolicy(idx, 'strictness', v)}
                >
                  {MODERATION_STRICTNESS.map((s) => (
                    <Select.Option key={s.value} value={s.value}>
                      {t(s.label)}
                    </Select.Option>
                  ))}
                </Select>
                <div style={{ marginTop: 4 }}>
                  <Text type='tertiary' size='small'>
                    {t(
                      MODERATION_STRICTNESS.find(
                        (s) => s.value === (p.strictness || 'standard'),
                      )?.desc || '',
                    )}
                  </Text>
                </div>
              </Col>
              <Col xs={24} sm={12} md={6}>
                <div style={{ marginBottom: 4 }}>&nbsp;</div>
                <Button type='danger' onClick={() => removePolicy(idx)}>
                  {t('删除策略')}
                </Button>
              </Col>
            </Row>
            <Table
              columns={categoryColumns(idx)}
              dataSource={MODERATION_POLICY_CATEGORIES}
              rowKey='value'
              pagination={false}
              size='small'
            />
          </Collapse.Panel>
        ))}
      </Collapse>

      <Button style={{ marginTop: 12 }} onClick={addPolicy}>
        {t('添加策略')}
      </Button>

      <Typography.Title heading={6} style={{ marginTop: 24, marginBottom: 8 }}>
        {t('分组绑定')}
      </Typography.Title>
      <Banner
        type='info'
        description={t(
          '用来做灰度：给测试分组开「仅观察」，其余分组保持「拦截」，就不必拿全站的关键词拦截去换观察数据。注意生效的是令牌分组（令牌未指定时才用用户分组），用户可以在自己有权限的分组之间切换——所以这里适合做灰度，不适合做分级管控。',
        )}
        style={{ marginBottom: 12 }}
      />
      <Table
        columns={[
          {
            title: t('分组'),
            dataIndex: 'name',
            width: 220,
            render: (name, r) => (
              <Space spacing={4}>
                <Text>{name}</Text>
                {r.orphan && (
                  <Tag color='red' shape='circle' size='small'>
                    {t('分组已删除')}
                  </Tag>
                )}
              </Space>
            ),
          },
          {
            title: t('运行模式'),
            dataIndex: 'mode',
            width: 180,
            render: (_, r) => (
              <Select
                value={groupPolicies[r.name]?.mode || ''}
                style={{ width: '100%' }}
                onChange={(v) => updateGroup(r.name, 'mode', v)}
              >
                {MODERATION_GROUP_MODES.map((m) => (
                  <Select.Option key={m.value || 'inherit'} value={m.value}>
                    {t(m.label)}
                  </Select.Option>
                ))}
              </Select>
            ),
          },
          {
            title: t('策略'),
            dataIndex: 'policy',
            width: 200,
            render: (_, r) => (
              <Select
                value={groupPolicies[r.name]?.policy || ''}
                style={{ width: '100%' }}
                onChange={(v) => updateGroup(r.name, 'policy', v)}
              >
                <Select.Option value=''>{t('使用默认策略')}</Select.Option>
                {policies
                  .filter((p) => (p.name || '').trim())
                  .map((p) => (
                    <Select.Option key={p.name} value={p.name}>
                      {p.name}
                    </Select.Option>
                  ))}
              </Select>
            ),
          },
        ]}
        dataSource={groupRows}
        rowKey='name'
        pagination={false}
        size='small'
      />

      <Button
        style={{ marginTop: 16 }}
        loading={loading}
        onClick={onSubmit}
        type='primary'
      >
        {t('保存审核策略')}
      </Button>
    </Card>
  );
}
