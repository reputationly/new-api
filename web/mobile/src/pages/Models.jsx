import React, { useContext, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  InfiniteScroll,
  NavBar,
  Popup,
  SearchBar,
  Tag,
} from 'antd-mobile';
import { FilterOutline } from 'antd-mobile-icons';

import { StatusContext } from '@classic/context/Status';
import { API } from '@classic/helpers/api';
// 直接引纯计算模块——helpers/utils.jsx 会传染桌面依赖、在 mobile 侧被整模块 shim，
// videoMatrix.js 不引 UI 依赖，两端共用同一份，不必再手抄同步。
import { flattenVideoMatrix } from '@classic/helpers/videoMatrix';
import { formatPriceWithCeiling } from '@classic/helpers/priceFormat';
import {
  MODEL_CATEGORIES,
  buildModelCategoryIndex,
  resolveModelCategory,
} from '@classic/constants/playgroundAdmin.constants';

import { showError } from '../shims/classic-utils';

const PAGE = 30;

// 模型广场：搜索 + 分组/大类筛选 + 紧凑列表 + 点击查看详情。
// 手机端不做供应商筛选（供应商有二十多家，胶囊铺满两屏还选不准），供应商信息在列表行里展示。
const Models = () => {
  const navigate = useNavigate();
  const [statusState] = useContext(StatusContext);
  const [models, setModels] = useState([]);
  const [vendors, setVendors] = useState([]);
  const [groupRatioMap, setGroupRatioMap] = useState({});
  // 分组内按模型的倍率。后端已展开通配并算完三层，这里只查表（同 PC getEffectiveGroupRatio）
  const [groupModelRatioMap, setGroupModelRatioMap] = useState({});
  const [usableGroupMap, setUsableGroupMap] = useState({});
  const [keyword, setKeyword] = useState('');
  const [group, setGroup] = useState('');
  const [category, setCategory] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const [detail, setDetail] = useState(null);
  const [filterVisible, setFilterVisible] = useState(false);

  useEffect(() => {
    const load = async () => {
      try {
        const res = await API.get('/api/pricing');
        if (res.data.success) {
          setModels(res.data.data || []);
          setVendors(res.data.vendors || []);
          setGroupRatioMap(res.data.group_ratio || {});
          setGroupModelRatioMap(res.data.group_model_ratio || {});
          setUsableGroupMap(res.data.usable_group || {});
        } else {
          showError(res.data.message);
        }
      } catch (e) {
        showError(e);
      }
    };
    load();
  }, []);

  const vendorName = (id) => vendors.find((v) => v.id === id)?.name || '';

  const siteStatus = useMemo(() => {
    if (statusState?.status) return statusState.status;
    try {
      return JSON.parse(localStorage.getItem('status') || '{}');
    } catch (e) {
      return {};
    }
  }, [statusState?.status]);

  // 大类索引来自 /api/status 的四份体验区 ModelConfig（与 PC 端同一判定）。
  const categoryIndex = useMemo(
    () => buildModelCategoryIndex(siteStatus),
    [siteStatus],
  );

  // 只列当前用户真能用的分组（/api/pricing 的 usable_group 已按用户分组裁剪）。
  // 与 enable_groups 取交集是为了不出现「点了没结果」的空胶囊——后端只按「模型至少
  // 命中一个可用分组」过滤模型，模型自身的 enable_groups 里仍可能带着用户无权的分组。
  const groups = useMemo(() => {
    const usable = new Set(
      Object.keys(usableGroupMap).filter((g) => g !== '' && g !== 'auto'),
    );
    const set = new Set();
    models.forEach((m) =>
      (m.enable_groups || []).forEach((g) => {
        if (usable.has(g)) set.add(g);
      }),
    );
    return Array.from(set);
  }, [models, usableGroupMap]);

  // 只保留本站真有模型的大类，避免出现点了没结果的空胶囊。
  const categories = useMemo(() => {
    const counted = new Set(
      models.map((m) => resolveModelCategory(m, categoryIndex)),
    );
    return MODEL_CATEGORIES.filter((c) => counted.has(c.key));
  }, [models, categoryIndex]);

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    return models.filter((m) => {
      if (kw && !(m.model_name || '').toLowerCase().includes(kw)) return false;
      if (group && !(m.enable_groups || []).includes(group)) return false;
      if (category && resolveModelCategory(m, categoryIndex) !== category)
        return false;
      return true;
    });
  }, [models, keyword, group, category, categoryIndex]);

  useEffect(() => {
    setLimit(PAGE);
  }, [keyword, group, category]);

  // 价格口径与 PC 端一致（helpers/utils.jsx getPriceData）：
  // 按量:输入价 = model_ratio × 2 × 分组倍率 → $/1M tokens；按次:价格 × 分组倍率。
  //
  // 分组倍率：选中胶囊时就用该分组；「全部分组」时与 PC 的「最优倍率视图」同源
  // （calculateModelPrice 里 selectedGroup === 'all' 的分支）——逐个模型在它自己的
  // enable_groups 里取最低倍率，而不是拿当前登录用户的分组去套：后者在用户分组倍率
  // 为 0 时，会把该分组根本挂不到渠道、压根用不了的模型也一并乘成 0 元。
  // 0 是合法倍率（免费分组），照常参与比较，不做剔除——同 helpers/videoMatrix.js 的结论。
  //
  // 分组倍率不再是整组一个数：管理员可给分组内单个模型配折扣，后端在 group_model_ratio
  // 里下发终值。比最低倍率时必须比**该模型在各分组下的实际倍率**，拿基础倍率挑出来的
  // 「最优分组」配了折扣后可能根本不是最便宜的那个。
  const effectiveRatio = (g, modelName) => {
    const perModel = groupModelRatioMap[g]?.[modelName];
    return perModel !== undefined ? perModel : groupRatioMap[g];
  };
  const resolveGroupRatio = (m) => {
    if (group)
      return { group, ratio: effectiveRatio(group, m.model_name) ?? 1 };
    const candidates = (m.enable_groups || [])
      .map((g) => ({ group: g, ratio: effectiveRatio(g, m.model_name) }))
      .filter((c) => typeof c.ratio === 'number');
    if (!candidates.length) return { group: '', ratio: 1 };
    return candidates.reduce((a, b) => (b.ratio < a.ratio ? b : a));
  };
  const currencyConfig = useMemo(() => {
    const displayType =
      siteStatus?.quota_display_type ||
      localStorage.getItem('quota_display_type') ||
      'USD';
    if (displayType === 'CNY') {
      return {
        symbol: '¥',
        rate: Number(siteStatus?.usd_exchange_rate) || 7.3,
      };
    }
    if (displayType === 'CUSTOM') {
      return {
        symbol: siteStatus?.custom_currency_symbol || '¤',
        rate: Number(siteStatus?.custom_currency_exchange_rate) || 1,
      };
    }
    return { symbol: '$', rate: 1 };
  }, [siteStatus]);

  // 动态计费（tiered_expr）：表达式才是定价事实，静态倍率会误导（同 PC 判定）
  const isDynamic = (m) => m.billing_mode === 'tiered_expr' && !!m.billing_expr;

  // 视频计费矩阵：实收按「分辨率 × 输入是否含视频」查表，与 model_ratio 无关——
  // 后者只是提交时的预扣锚点。不单独判的话列表会显示锚点价（480p 实际 ¥46 却显示
  // ¥51），详情还会算出一个「输出价格」——视频模型根本没有输出价这回事。
  // 判定与 PC 端 calculateModelPrice 同源，见 docs/video-billing-matrix-design.md §2.6。
  const isVideoMatrix = (m) => !!m.video_pricing?.mode;

  // 直接复用 PC 端的摊平实现，不再抄一份：它已处理好「0 倍率是合法值」与
  // 「按折算前的原始价过滤未配置格子」两个坑，抄一份就意味着以后要修两遍
  // ——排序那个 bug 就是因为抄了三份才出现三次。
  const videoMatrixCells = (m) =>
    flattenVideoMatrix(m.video_pricing, resolveGroupRatio(m).ratio);

  // 与 PC 端共用同一份格式化（含「≥0.001 向上取整、更小则四舍五入」的规则）。
  // 原先这里是手抄的一份拷贝，两端各改一次就会漂移。
  const formatPrice = formatPriceWithCeiling;

  const inputPricePerM = (m) => m.model_ratio * 2 * resolveGroupRatio(m).ratio;
  const displayPrice = (usdValue) =>
    `${currencyConfig.symbol}${formatPrice(usdValue * currencyConfig.rate)}`;

  const priceText = (m) => {
    if (isDynamic(m)) return '动态计费';
    if (isVideoMatrix(m)) {
      // 矩阵有多格，列表里放不下，给区间；逐格价目在详情里。
      const cells = videoMatrixCells(m);
      if (!cells.length) return '场景计费';
      const prices = cells.map((c) => c.priceUSD);
      const lo = Math.min(...prices);
      const hi = Math.max(...prices);
      const unit = m.video_pricing.mode === 'token' ? '/1M' : '/次';
      return lo === hi
        ? `${displayPrice(lo)}${unit}`
        : `${displayPrice(lo)}~${displayPrice(hi)}${unit}`;
    }
    return m.quota_type === 1
      ? `${displayPrice(m.model_price * resolveGroupRatio(m).ratio)}/次`
      : `${displayPrice(inputPricePerM(m))}/1M`;
  };
  const detailPricingGroup = detail
    ? resolveGroupRatio(detail)
    : { group: '', ratio: 1 };
  // 详情里的「可用分组」同样只列用户有权的那部分（同 PC ModelPricingTable 的取交集）
  const detailUsableGroups = (detail?.enable_groups || []).filter(
    (g) => g !== '' && g !== 'auto' && usableGroupMap[g] !== undefined,
  );
  const activeFilterCount = Number(Boolean(group)) + Number(Boolean(category));
  const selectedCategoryLabel =
    MODEL_CATEGORIES.find((c) => c.key === category)?.label || '全部大类';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar onBack={() => navigate(-1)}>模型广场</NavBar>
      <div style={{ background: '#fff', paddingBottom: 2 }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '10px 12px 6px',
          }}
        >
          <SearchBar
            placeholder='搜索模型名称'
            value={keyword}
            onChange={setKeyword}
            style={{ flex: 1 }}
          />
          <Button
            size='small'
            fill='outline'
            onClick={() => setFilterVisible(true)}
            style={{
              '--border-radius': '18px',
              flexShrink: 0,
              height: 36,
              padding: '0 12px',
            }}
          >
            <span
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 4,
              }}
            >
              <FilterOutline />
              筛选{activeFilterCount > 0 ? ` ${activeFilterCount}` : ''}
            </span>
          </Button>
        </div>
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            gap: 8,
            padding: '0 14px 8px',
            color: '#9aa1ad',
            fontSize: 11.5,
          }}
        >
          <span
            style={{
              minWidth: 0,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {group || '全部分组'} · {selectedCategoryLabel}
          </span>
          <span style={{ flexShrink: 0 }}>{filtered.length} 个模型</span>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto' }}>
        <div
          style={{
            margin: 12,
            borderRadius: 'var(--card-radius)',
            overflow: 'hidden',
            border: 'var(--card-border)',
            boxShadow: 'var(--card-shadow)',
            background: '#fff',
          }}
        >
          {filtered.slice(0, limit).map((m, idx) => (
            <div
              key={m.model_name}
              onClick={() => setDetail(m)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                padding: '13px 14px',
                borderTop: idx === 0 ? 'none' : '0.5px solid #f0f1f5',
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  style={{
                    fontWeight: 500,
                    fontSize: 14.5,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {m.model_name}
                </div>
                {vendorName(m.vendor_id) && (
                  <div
                    style={{ fontSize: 11.5, color: '#9aa1ad', marginTop: 2 }}
                  >
                    {vendorName(m.vendor_id)}
                  </div>
                )}
              </div>
              <div
                style={{
                  flexShrink: 0,
                  fontSize: 13,
                  color: 'var(--brand-primary)',
                  fontVariantNumeric: 'tabular-nums',
                }}
              >
                {priceText(m)}
              </div>
            </div>
          ))}
          {filtered.length === 0 && (
            <p style={{ textAlign: 'center', color: '#9aa1ad', padding: 32 }}>
              没有匹配的模型
            </p>
          )}
        </div>
        <InfiniteScroll
          loadMore={async () => setLimit((l) => l + PAGE)}
          hasMore={limit < filtered.length}
        />
      </div>

      <Popup
        visible={filterVisible}
        onMaskClick={() => setFilterVisible(false)}
        bodyStyle={{
          maxHeight: '72vh',
          overflowY: 'auto',
          borderTopLeftRadius: 20,
          borderTopRightRadius: 20,
          padding: 16,
          paddingBottom: 'calc(16px + var(--safe-area-inset-bottom))',
        }}
      >
        <div style={{ fontWeight: 600, fontSize: 16, marginBottom: 16 }}>
          筛选模型
        </div>
        {categories.length > 0 && (
          <div style={{ marginBottom: 18 }}>
            <div style={{ fontSize: 12, color: '#9aa1ad', marginBottom: 8 }}>
              模型大类
            </div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <div
                className={`m-config-chip${category === '' ? ' active' : ''}`}
                onClick={() => setCategory('')}
              >
                全部大类
              </div>
              {categories.map((item) => (
                <div
                  key={item.key}
                  className={`m-config-chip${category === item.key ? ' active' : ''}`}
                  onClick={() => setCategory(item.key)}
                >
                  {item.label}
                </div>
              ))}
            </div>
          </div>
        )}
        {groups.length > 0 && (
          <div style={{ marginBottom: 20 }}>
            <div style={{ fontSize: 12, color: '#9aa1ad', marginBottom: 8 }}>
              分组
            </div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <div
                className={`m-config-chip${group === '' ? ' active' : ''}`}
                onClick={() => setGroup('')}
              >
                全部分组
              </div>
              {groups.map((item) => (
                <div
                  key={item}
                  className={`m-config-chip${group === item ? ' active' : ''}`}
                  onClick={() => setGroup(item)}
                >
                  {item}
                </div>
              ))}
            </div>
          </div>
        )}
        <div style={{ display: 'flex', gap: 10 }}>
          <Button
            block
            fill='outline'
            onClick={() => {
              setGroup('');
              setCategory('');
            }}
          >
            重置
          </Button>
          <Button block color='primary' onClick={() => setFilterVisible(false)}>
            完成
          </Button>
        </div>
      </Popup>

      <Popup
        visible={!!detail}
        onMaskClick={() => setDetail(null)}
        bodyStyle={{
          borderTopLeftRadius: 20,
          borderTopRightRadius: 20,
          padding: 16,
          paddingBottom: 'calc(16px + var(--safe-area-inset-bottom))',
        }}
      >
        {detail && (
          <div>
            <div
              style={{ fontWeight: 600, fontSize: 16, wordBreak: 'break-all' }}
            >
              {detail.model_name}
            </div>
            <div
              style={{
                marginTop: 8,
                display: 'flex',
                gap: 6,
                flexWrap: 'wrap',
              }}
            >
              <span
                className={`m-badge ${detail.quota_type === 1 ? 'pending' : 'info'}`}
              >
                {isDynamic(detail)
                  ? '动态计费'
                  : isVideoMatrix(detail)
                    ? '场景计费'
                    : detail.quota_type === 1
                      ? '按次计费'
                      : '按量计费'}
              </span>
              {vendorName(detail.vendor_id) && (
                <span className='m-badge info'>
                  {vendorName(detail.vendor_id)}
                </span>
              )}
            </div>
            {isVideoMatrix(detail) ? (
              // 交叉表：纵轴分辨率、横轴场景。与配置页和 PC 端同一版式，
              // 手机窄屏下按行堆叠更好读，故用「分辨率 + 场景」两列。
              <div style={{ marginTop: 12 }}>
                <div
                  style={{ fontSize: 12, color: '#9aa1ad', marginBottom: 6 }}
                >
                  单位：{currencyConfig.symbol} /{' '}
                  {detail.video_pricing.mode === 'token' ? '1M tokens' : '次'}
                </div>
                {videoMatrixCells(detail).map((cell) => (
                  <div
                    key={`${cell.resolution}-${cell.column}`}
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'baseline',
                      fontSize: 14,
                      color: '#374151',
                      padding: '4px 0',
                      borderBottom: '1px solid #f1f2f4',
                    }}
                  >
                    <span>
                      <strong>{cell.resolution}</strong>
                      <span style={{ color: '#9aa1ad', marginLeft: 6 }}>
                        {detail.video_pricing.mode === 'token'
                          ? cell.column === 'without_video'
                            ? '输入不含视频'
                            : '输入包含视频'
                          : `${cell.column} 秒`}
                      </span>
                    </span>
                    <strong>{displayPrice(cell.priceUSD)}</strong>
                  </div>
                ))}
              </div>
            ) : (
              <div style={{ fontSize: 14, color: '#374151', marginTop: 12 }}>
                {isDynamic(detail)
                  ? '动态计费：按用量阶梯表达式实时计算，详细规则请在电脑端模型广场查看'
                  : detail.quota_type === 1
                    ? `单次价格：${displayPrice(detail.model_price * detailPricingGroup.ratio)}`
                    : `输入 ${displayPrice(inputPricePerM(detail))} / 1M Tokens · 输出 ${displayPrice(inputPricePerM(detail) * (detail.completion_ratio || 1))} / 1M Tokens`}
              </div>
            )}
            <div style={{ fontSize: 12, color: '#9aa1ad', marginTop: 6 }}>
              按「{detailPricingGroup.group || '默认'}」分组倍率 ×
              {detailPricingGroup.ratio} 计算
              {!group && detailPricingGroup.group
                ? '（全部分组取最优倍率）'
                : ''}
            </div>
            {detail.description && (
              <div style={{ fontSize: 13, color: '#6b7280', marginTop: 8 }}>
                {detail.description}
              </div>
            )}
            {detailUsableGroups.length > 0 && (
              <div style={{ marginTop: 12 }}>
                <div
                  style={{ fontSize: 12, color: '#9aa1ad', marginBottom: 6 }}
                >
                  可用分组
                </div>
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  {detailUsableGroups.map((g) => (
                    <Tag key={g} color='default' fill='outline'>
                      {g}
                    </Tag>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </Popup>
    </div>
  );
};

export default Models;
