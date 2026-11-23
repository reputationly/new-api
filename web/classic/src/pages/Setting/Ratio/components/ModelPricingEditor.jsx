/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Input,
  Modal,
  Radio,
  RadioGroup,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconDelete,
  IconPlus,
  IconSave,
  IconSearch,
} from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { showError } from '../../../../helpers';
import ModelTimePriceSection from './ModelTimePriceSection';
import PriceInput from './PriceInput';
import {
  PAGE_SIZE,
  NUMERIC_INPUT_REGEX,
  VIDEO_MODE_TOKEN,
  VIDEO_MODE_PER_SECOND,
  buildSummaryText,
  canUseTokenVideoMatrix,
  videoResolutionOptionsForModel,
  hasValue,
  useModelPricingEditorState,
} from '../hooks/useModelPricingEditorState';
import { useIsMobile } from '../../../../hooks/common/useIsMobile';
import TieredPricingEditor from './TieredPricingEditor';
import VideoMatrixEditor from './VideoMatrixEditor';

const { Text } = Typography;
const EMPTY_CANDIDATE_MODEL_NAMES = [];

// 「按美元目录价录入」开关：纯录入体验，不改任何存储口径。
// 开关与汇率只是运营的记事本能用就行，放 localStorage，不占后端 option。
// 「按美元目录价录入」是**按模型**记的：不同模型的源货币可能不同（有的上游美元、
// 有的本来就人民币），做成全局开关会让切换模型时状态互相污染——之前就是这个毛病：
// 给一个模型打开后，切到任何模型都还是"美元"，看起来像"所有模型都变成美元计价"。
const USD_ENTRY_MAP_KEY = 'model_pricing_usd_entry_map';

const readUSDEntryMap = () => {
  try {
    const raw = JSON.parse(localStorage.getItem(USD_ENTRY_MAP_KEY) || '{}');
    return raw && typeof raw === 'object' && !Array.isArray(raw) ? raw : {};
  } catch (e) {
    return {};
  }
};

export default function ModelPricingEditor({
  options,
  refresh,
  candidateModelNames = EMPTY_CANDIDATE_MODEL_NAMES,
  filterMode = 'all',
  allowAddModel = true,
  allowDeleteModel = true,
  showConflictFilter = true,
  listDescription = '',
  emptyTitle = '',
  emptyDescription = '',
  timeDraft = '{}',
  onTimeDraftChange,
  saveTimePrice,
}) {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const [addVisible, setAddVisible] = useState(false);
  const [batchVisible, setBatchVisible] = useState(false);
  const [newModelName, setNewModelName] = useState('');

  // 美元目录价录入：只换算输入框的显示单位，落库仍是人民币（见 PriceInput 顶部注释）。
  //
  // 汇率是**每个模型自己的**（ModelSourceRate）：不同供应商/模型可能给不同汇率，
  // 改它时会按比例缩放该模型的价格与表达式系数（见 handleSourceRateChange），
  // 所以运营只需要改这一个数字，不用手改每个系数。
  const [usdEntryMap, setUsdEntryMap] = useState(readUSDEntryMap);
  useEffect(() => {
    try {
      localStorage.setItem(USD_ENTRY_MAP_KEY, JSON.stringify(usdEntryMap));
    } catch (e) {
      // 隐私模式写不进去也不影响本次录入。
    }
  }, [usdEntryMap]);

  const {
    selectedModel,
    selectedModelName,
    selectedModelNames,
    setSelectedModelName,
    setSelectedModelNames,
    searchText,
    setSearchText,
    currentPage,
    setCurrentPage,
    loading,
    conflictOnly,
    setConflictOnly,
    filteredModels,
    pagedData,
    selectedWarnings,
    previewRows,
    isOptionalFieldEnabled,
    handleOptionalFieldToggle,
    handleNumericFieldChange,
    dirty,
    handleSourceRateChange,
    handleBillingModeChange,
    handleBillingExprChange,
    handleRequestRuleExprChange,
    handleVideoMatrixChange,
    handleVideoMatrixToggle,
    handleSubmit,
    addModel,
    deleteModel,
    applySelectedModelPricing,
  } = useModelPricingEditorState({
    options,
    refresh,
    t,
    candidateModelNames,
    filterMode,
  });

  // 这里只做「美元 → 人民币」的汇率折算，不碰任何折扣：
  // 分组折扣 / 分组倍率由分组管理配置，在这份人民币价上继续乘。
  // 汇率是每个模型自己的（ModelSourceRate），必须在 hook 解构之后才能读。
  const modelSourceRate = Number(selectedModel?.sourceRate);
  // 汇率输入是「草稿 + 失焦提交」：确认是有效正数才一次性换算价格与系数。
  const [rateDraft, setRateDraft] = useState('');
  useEffect(() => {
    setRateDraft(selectedModel?.sourceRate ?? '');
  }, [selectedModel?.name, selectedModel?.sourceRate]);
  const commitRateDraft = () => {
    if (!selectedModel) return;
    const stored = selectedModel.sourceRate ?? '';
    const next = String(rateDraft).trim();
    const num = Number(next);
    if (next === stored) return;
    if (!Number.isFinite(num) || num <= 0) {
      // 非法（空 / 0 / 非数字）：回到已保存的值，不换算。
      setRateDraft(stored);
      return;
    }
    handleSourceRateChange(next);
  };
  // **默认全部按人民币显示**，谁要用美元入录谁自己打开（按模型记住）。
  // 不做「有汇率就默认美元」——那会让所有配过汇率的模型一进来就是美元，
  // 运营会以为全站被切成了美元计价。
  const usdEntry = selectedModel
    ? Boolean(usdEntryMap[selectedModel.name])
    : false;
  const setUsdEntry = (checked) => {
    if (!selectedModel) return;
    setUsdEntryMap((prev) => ({ ...prev, [selectedModel.name]: !!checked }));
  };
  const effectiveUsdRate =
    usdEntry && Number.isFinite(modelSourceRate) && modelSourceRate > 0
      ? modelSourceRate
      : null;

  // 分时价草稿：时段模板来自顶部的折叠栏，这里只管"当前模型绑了哪些档、各档多少钱"。
  const timeCfg = useMemo(() => {
    try {
      const parsed = JSON.parse(timeDraft || '{}') || {};
      return { windows: parsed.windows || {}, rules: parsed.rules || {} };
    } catch (e) {
      return { windows: {}, rules: {} };
    }
  }, [timeDraft]);

  const basePriceOf = useCallback(
    (name) => {
      const parse = (key) => {
        try {
          return JSON.parse((options && options[key]) || '{}') || {};
        } catch (e) {
          return {};
        }
      };
      const modelRatio = Number(parse('ModelRatio')[name]) || 0;
      const ratioOf = (key) => {
        const v = Number(parse(key)[name]);
        return Number.isFinite(v) && v > 0 ? v : null;
      };
      const completion = ratioOf('CompletionRatio');
      const audio = ratioOf('AudioRatio');
      const audioCompletion = ratioOf('AudioCompletionRatio');
      const cache = ratioOf('CacheRatio');
      const createCache = ratioOf('CreateCacheRatio');
      const image = ratioOf('ImageRatio');
      const perCall = Number(parse('ModelPrice')[name]) || 0;
      return {
        input: modelRatio > 0 ? modelRatio : null,
        output: modelRatio > 0 && completion ? modelRatio * completion : null,
        cache: modelRatio > 0 && cache ? modelRatio * cache : null,
        create_cache:
          modelRatio > 0 && createCache ? modelRatio * createCache : null,
        image: modelRatio > 0 && image ? modelRatio * image : null,
        audio_input: modelRatio > 0 && audio ? modelRatio * audio : null,
        audio_output:
          modelRatio > 0 && audio && audioCompletion
            ? modelRatio * audio * audioCompletion
            : null,
        per_call: perCall > 0 ? perCall : null,
      };
    },
    [options],
  );

  const setModelTimeRules = useCallback(
    (name, rules) => {
      if (!onTimeDraftChange) return;
      const nextRules = { ...timeCfg.rules };
      const global = { ...(nextRules['*'] || {}) };
      if (!rules || rules.length === 0) {
        delete global[name];
      } else {
        global[name] = rules;
      }
      if (Object.keys(global).length > 0) {
        nextRules['*'] = global;
      } else {
        delete nextRules['*'];
      }
      onTimeDraftChange(
        JSON.stringify({ windows: timeCfg.windows, rules: nextRules }, null, 2),
      );
    },
    [onTimeDraftChange, timeCfg],
  );

  // 「应用更改」：先落分时价（另一个 option key），再落价格。两段分别报错，
  // 不把"分时价保存失败"和"价格保存失败"混成一句。
  const handleApply = useCallback(async () => {
    if (saveTimePrice) {
      try {
        await saveTimePrice();
      } catch (error) {
        showError(error.message || t('保存分时价失败'));
        return;
      }
    }
    await handleSubmit();
  }, [saveTimePrice, handleSubmit, t]);

  const getExprModeLabel = useCallback(
    (model) => {
      if (model?.billingMode !== 'tiered_expr') {
        return '';
      }
      return (model.billingExpr || '').includes('tier(')
        ? t('阶梯计费')
        : t('表达式计费');
    },
    [t],
  );

  const columns = useMemo(
    () => [
      {
        title: t('模型名称'),
        dataIndex: 'name',
        key: 'name',
        render: (text, record) => (
          <Space>
            <Button
              theme='borderless'
              type='tertiary'
              onClick={() => setSelectedModelName(record.name)}
              style={{
                padding: 0,
                color:
                  record.name === selectedModelName
                    ? 'var(--semi-color-primary)'
                    : undefined,
              }}
            >
              {text}
            </Button>
            {selectedModelNames.includes(record.name) ? (
              <Tag color='green' shape='circle'>
                {t('已勾选')}
              </Tag>
            ) : null}
            {record.hasConflict ? (
              <Tag color='red' shape='circle'>
                {t('矛盾')}
              </Tag>
            ) : null}
          </Space>
        ),
      },
      {
        title: t('计费方式'),
        dataIndex: 'billingMode',
        key: 'billingMode',
        render: (_, record) => (
          <Tag
            color={
              record.billingMode === 'per-request'
                ? 'teal'
                : record.billingMode === 'tiered_expr'
                  ? 'amber'
                  : 'violet'
            }
          >
            {record.billingMode === 'per-request'
              ? t('按次计费')
              : record.billingMode === 'tiered_expr'
                ? getExprModeLabel(record)
                : t('按量计费')}
          </Tag>
        ),
      },
      {
        title: t('价格摘要'),
        dataIndex: 'summary',
        key: 'summary',
        render: (_, record) => buildSummaryText(record, t),
      },
      {
        title: t('分时价'),
        key: 'timePrice',
        width: 90,
        render: (_, record) => {
          const count = ((timeCfg.rules['*'] || {})[record.name] || []).length;
          return count > 0 ? (
            <Tag color='cyan' size='small'>
              {t('{{n}} 档', { n: count })}
            </Tag>
          ) : (
            <span style={{ color: 'var(--semi-color-text-2)' }}>-</span>
          );
        },
      },
      {
        title: t('操作'),
        key: 'action',
        render: (_, record) => (
          <Space>
            {allowDeleteModel ? (
              <Button
                size='small'
                type='danger'
                icon={<IconDelete />}
                onClick={() => deleteModel(record.name)}
              />
            ) : null}
          </Space>
        ),
      },
    ],
    [
      allowDeleteModel,
      deleteModel,
      getExprModeLabel,
      selectedModelName,
      selectedModelNames,
      setSelectedModelName,
      t,
      timeCfg,
    ],
  );

  const handleAddModel = () => {
    if (addModel(newModelName)) {
      setNewModelName('');
      setAddVisible(false);
    }
  };

  const rowSelection = {
    selectedRowKeys: selectedModelNames,
    onChange: (selectedRowKeys) => setSelectedModelNames(selectedRowKeys),
  };

  // 分时价卡片：**按量**与**按次**都支持（后端白名单里有 per_call 分量），
  // 只有表达式计费互斥——表达式自己能用 hour() 表达时段，两边都乘会双计。
  // JSX props 在元素创建时就求值，selectedModel 为空（未选模型）时必须短路，
  // 否则整个编辑器在渲染期抛 null.name，把页面带崩。
  const timePriceCard = selectedModel ? (
    <Card bodyStyle={{ padding: 16 }} style={{ marginBottom: 16 }}>
      <ModelTimePriceSection
        modelName={selectedModel.name}
        windows={timeCfg.windows}
        rules={(timeCfg.rules['*'] || {})[selectedModel.name] || []}
        base={basePriceOf(selectedModel.name)}
        onChange={(rules) => setModelTimeRules(selectedModel.name, rules)}
        allRules={timeCfg.rules}
        usdRate={effectiveUsdRate}
      />
    </Card>
  ) : null;

  return (
    <>
      <Space vertical align='start' style={{ width: '100%' }}>
        <Banner
          type='info'
          bordered
          fullMode={false}
          closeIcon={null}
          style={{ width: '100%' }}
          description={t(
            '价格单位统一为人民币（¥）：输入价是 ¥/百万 tokens，按次价是 ¥/次，视频矩阵是 ¥/秒 或 ¥/次。填什么就存什么，不再按汇率折算；下方的「按美元目录价录入」只把输入框换成 $ 显示，保存时仍折算成人民币。价格显示保留 2 位小数，存储仍为高精度，计费不受影响。',
          )}
        />
        <Space wrap className='mt-2'>
          {allowAddModel ? (
            <Button
              icon={<IconPlus />}
              onClick={() => setAddVisible(true)}
              style={isMobile ? { width: '100%' } : undefined}
            >
              {t('添加模型')}
            </Button>
          ) : null}
          <Button
            type='primary'
            icon={<IconSave />}
            loading={loading}
            onClick={handleApply}
            style={isMobile ? { width: '100%' } : undefined}
          >
            {t('应用更改')}
          </Button>
          {dirty ? (
            <Tag color='orange' shape='circle' size='small'>
              {t('有未保存的更改')}
            </Tag>
          ) : null}
          <Button
            disabled={!selectedModel || selectedModelNames.length === 0}
            onClick={() => setBatchVisible(true)}
            style={isMobile ? { width: '100%' } : undefined}
          >
            {t('批量应用当前模型价格')}
            {selectedModelNames.length > 0
              ? ` (${selectedModelNames.length})`
              : ''}
          </Button>
          <Input
            prefix={<IconSearch />}
            placeholder={t('搜索模型名称')}
            value={searchText}
            onChange={(value) => setSearchText(value)}
            style={{ width: isMobile ? '100%' : 220 }}
            showClear
          />
          {showConflictFilter ? (
            <Checkbox
              checked={conflictOnly}
              onChange={(event) => setConflictOnly(event.target.checked)}
            >
              {t('仅显示矛盾倍率')}
            </Checkbox>
          ) : null}
        </Space>

        {listDescription ? (
          <div className='text-sm text-gray-500'>{listDescription}</div>
        ) : null}
        {selectedModelNames.length > 0 ? (
          <div
            style={{
              width: '100%',
              padding: '10px 12px',
              borderRadius: 8,
              background: 'var(--semi-color-primary-light-default)',
              border: '1px solid var(--semi-color-primary)',
              color: 'var(--semi-color-primary)',
              fontWeight: 600,
            }}
          >
            {t('已勾选 {{count}} 个模型', { count: selectedModelNames.length })}
          </div>
        ) : null}

        <div
          style={{
            width: '100%',
            display: 'grid',
            gap: 16,
            gridTemplateColumns: isMobile
              ? 'minmax(0, 1fr)'
              : 'minmax(300px, 0.8fr) minmax(480px, 1.2fr)',
          }}
        >
          <Card
            bodyStyle={{ padding: 0 }}
            style={isMobile ? { order: 2 } : undefined}
          >
            <div style={{ overflowX: 'auto' }}>
              <Table
                columns={columns}
                dataSource={pagedData}
                rowKey='name'
                rowSelection={rowSelection}
                pagination={{
                  currentPage,
                  pageSize: PAGE_SIZE,
                  total: filteredModels.length,
                  onPageChange: (page) => setCurrentPage(page),
                  showTotal: true,
                  showSizeChanger: false,
                }}
                empty={
                  <div style={{ textAlign: 'center', padding: '20px' }}>
                    {emptyTitle || t('暂无模型')}
                  </div>
                }
                onRow={(record) => ({
                  style: {
                    background: selectedModelNames.includes(record.name)
                      ? 'var(--semi-color-success-light-default)'
                      : record.name === selectedModelName
                        ? 'var(--semi-color-primary-light-default)'
                        : undefined,
                    boxShadow: selectedModelNames.includes(record.name)
                      ? 'inset 4px 0 0 var(--semi-color-success)'
                      : record.name === selectedModelName
                        ? 'inset 4px 0 0 var(--semi-color-primary)'
                        : undefined,
                    transition: 'background 0.2s ease, box-shadow 0.2s ease',
                  },
                  onClick: () => setSelectedModelName(record.name),
                })}
                scroll={isMobile ? { x: 720 } : undefined}
              />
            </div>
          </Card>

          <Card
            style={isMobile ? { order: 1 } : undefined}
            title={selectedModel ? selectedModel.name : t('模型计费编辑器')}
            headerExtraContent={
              selectedModel ? (
                <Space>
                  <Tag
                    color={
                      selectedModel.billingMode === 'per-request'
                        ? 'teal'
                        : selectedModel.billingMode === 'tiered_expr'
                          ? 'amber'
                          : 'blue'
                    }
                  >
                    {selectedModel.billingMode === 'per-request'
                      ? t('按次计费')
                      : selectedModel.billingMode === 'tiered_expr'
                        ? getExprModeLabel(selectedModel)
                        : t('按量计费')}
                  </Tag>
                  {selectedModel.videoMatrix ? (
                    <Tag color='violet'>{t('视频矩阵')}</Tag>
                  ) : null}
                </Space>
              ) : null
            }
          >
            {!selectedModel ? (
              <Empty
                title={emptyTitle || t('暂无模型')}
                description={
                  emptyDescription || t('请先新增模型或从左侧列表选择一个模型')
                }
              />
            ) : (
              <div>
                <div className='mb-4'>
                  <div className='mb-2 font-medium text-gray-700'>
                    {t('计费方式')}
                  </div>
                  <RadioGroup
                    type='button'
                    value={selectedModel.billingMode}
                    onChange={(event) =>
                      handleBillingModeChange(event.target.value)
                    }
                  >
                    <Radio value='per-token'>{t('按量计费')}</Radio>
                    <Radio value='per-request'>{t('按次计费')}</Radio>
                    <Radio value='tiered_expr'>{t('表达式/阶梯计费')}</Radio>
                  </RadioGroup>
                  <div className='mt-2 text-xs text-gray-500'>
                    {t(
                      '普通按量/按次直接填价格就行；如果价格要跟请求参数或请求头联动，请切到表达式/阶梯计费。',
                    )}
                  </div>
                  {/*
                    源货币汇率：**始终显示**。它是每个模型自己的参数，改它会把该模型的
                    价格与表达式系数按比例重算——表达式模式没有价格框、也没有美元开关，
                    如果继续把它藏在开关后面，改汇率这条路就断了。
                  */}
                  <div className='mt-3 flex flex-wrap items-center gap-3'>
                    <span className='text-sm font-medium text-gray-700'>
                      {t('源货币汇率')}
                    </span>
                    <Input
                      size='small'
                      style={{ width: 140 }}
                      value={rateDraft}
                      placeholder='6.8'
                      suffix={<span className='text-xs'>¥/$</span>}
                      onChange={(v) => {
                        // 只改草稿：逐键换算会拿"上一次输入"当分母，
                        // 清空再填 7 不会缩放、中间经过 0 会把价格算成空。
                        if (NUMERIC_INPUT_REGEX.test(v)) {
                          setRateDraft(v);
                        }
                      }}
                      onBlur={commitRateDraft}
                      onEnterPress={commitRateDraft}
                    />
                    <span className='text-xs text-gray-500'>
                      {t(
                        '供应商按美元报价时，这里填它的结算汇率。改这个数字会把本模型的价格与表达式系数按比例重算（先点「应用更改」才生效）。',
                      )}
                    </span>
                  </div>
                  {/* 录入单位开关：只换算下面价格框的显示与输入，落库口径一个字不变。
                      表达式模式没有价格框，开关对它没有意义，直接不显示。 */}
                  {selectedModel.billingMode !== 'tiered_expr' ? (
                    <>
                      <div className='mt-3 flex flex-wrap items-center gap-3'>
                        <Switch
                          size='small'
                          checked={usdEntry}
                          onChange={setUsdEntry}
                        />
                        <span className='text-sm font-medium text-gray-700'>
                          {t('按美元目录价录入')}
                        </span>
                      </div>
                      {usdEntry ? (
                        <div className='mt-2 text-xs text-gray-500'>
                          {t(
                            '下面价格框改为 $/1M tokens 或 $/次，直接照抄供应商目录价；保存时按上方「源货币汇率」折算成人民币入库。分组折扣 / 分组倍率在分组管理里配置。',
                          )}
                        </div>
                      ) : null}
                    </>
                  ) : null}
                  {!Number(selectedModel.sourceRate) ? (
                    <div
                      className='mt-1 text-xs'
                      style={{ color: 'var(--semi-color-warning)' }}
                    >
                      {t(
                        '该模型还没有记录源汇率：先填当前汇率（如 6.8）并点「应用更改」保存一次；之后改汇率才会自动按比例重算价格与表达式系数。',
                      )}
                    </div>
                  ) : null}
                </div>

                {selectedWarnings.length > 0 ? (
                  <Card
                    bodyStyle={{ padding: 12 }}
                    style={{
                      marginBottom: 16,
                      background: 'var(--semi-color-warning-light-default)',
                    }}
                  >
                    <div className='font-medium mb-2'>{t('当前提示')}</div>
                    {selectedWarnings.map((warning) => (
                      <div key={warning} className='text-sm text-gray-700 mb-1'>
                        {warning}
                      </div>
                    ))}
                  </Card>
                ) : null}

                {selectedModel.billingMode === 'per-request' ? (
                  <>
                    <PriceInput
                      label={t('固定价格')}
                      value={selectedModel.fixedPrice}
                      usdRate={effectiveUsdRate}
                      placeholder={t('输入每次调用价格')}
                      suffix={t('¥/次')}
                      onChange={(value) =>
                        handleNumericFieldChange('fixedPrice', value)
                      }
                      extraText={t('适合 MJ / 任务类等按次收费模型。')}
                    />
                    {timePriceCard}
                  </>
                ) : selectedModel.billingMode === 'tiered_expr' ? (
                  <>
                    <Banner
                      type='info'
                      closeIcon={null}
                      style={{ marginBottom: 16 }}
                      description={t(
                        '表达式计费没有「分时价」：它自己能按 hour() / weekday() 表达时段，两边同时乘会把时间系数算两遍。要按时段改价，请在下面的表达式里写，或改用按量计费 + 分时价。',
                      )}
                    />
                    {usdEntry ? (
                      <Banner
                        type='warning'
                        closeIcon={null}
                        style={{ marginBottom: 16 }}
                        description={t(
                          '表达式里的系数固定按 ¥/百万 tokens 填写：系数 = 美元价 × 本模型汇率（{{rate}}）。改汇率时这些系数会按比例自动重算。',
                          { rate: selectedModel.sourceRate || t('未填') },
                        )}
                      />
                    ) : null}
                    <TieredPricingEditor
                      model={selectedModel}
                      onExprChange={handleBillingExprChange}
                      requestRuleExpr={selectedModel.requestRuleExpr}
                      onRequestRuleExprChange={handleRequestRuleExprChange}
                      t={t}
                    />
                  </>
                ) : (
                  <>
                    <Card
                      bodyStyle={{ padding: 16 }}
                      style={{
                        marginBottom: 16,
                        background: 'var(--semi-color-fill-0)',
                      }}
                    >
                      <div className='font-medium mb-3'>{t('基础价格')}</div>
                      <PriceInput
                        label={t('输入价格')}
                        value={selectedModel.inputPrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('inputPrice', value)
                        }
                      />
                      {selectedModel.completionRatioLocked ? (
                        <Banner
                          type='warning'
                          bordered
                          fullMode={false}
                          closeIcon={null}
                          style={{ marginBottom: 12 }}
                          title={t('输出价格已锁定')}
                          description={t(
                            '该模型补全倍率由后端固定为 {{ratio}}。输出价格不能在这里修改。',
                            {
                              ratio: selectedModel.lockedCompletionRatio || '-',
                            },
                          )}
                        />
                      ) : null}
                      <PriceInput
                        label={t('输出价格')}
                        value={selectedModel.completionPrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('completionPrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'completionPrice',
                            )}
                            disabled={selectedModel.completionRatioLocked}
                            onChange={(checked) =>
                              handleOptionalFieldToggle(
                                'completionPrice',
                                checked,
                              )
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'completionPrice',
                          )
                        }
                        disabled={
                          !hasValue(selectedModel.inputPrice) ||
                          selectedModel.completionRatioLocked
                        }
                        extraText={
                          selectedModel.completionRatioLocked
                            ? t(
                                '后端固定倍率：{{ratio}}。该字段仅展示换算后的价格。',
                                {
                                  ratio:
                                    selectedModel.lockedCompletionRatio || '-',
                                },
                              )
                            : !isOptionalFieldEnabled(
                                  selectedModel,
                                  'completionPrice',
                                )
                              ? t('当前未启用，需要时再打开即可。')
                              : ''
                        }
                      />
                      <PriceInput
                        label={t('缓存命中价格')}
                        value={selectedModel.cachePrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('cachePrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'cachePrice',
                            )}
                            onChange={(checked) =>
                              handleOptionalFieldToggle('cachePrice', checked)
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(selectedModel, 'cachePrice')
                        }
                        disabled={!hasValue(selectedModel.inputPrice)}
                        extraText={
                          !isOptionalFieldEnabled(selectedModel, 'cachePrice')
                            ? t('当前未启用，需要时再打开即可。')
                            : ''
                        }
                      />
                      <PriceInput
                        label={t('缓存创建价格')}
                        value={selectedModel.createCachePrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('createCachePrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'createCachePrice',
                            )}
                            onChange={(checked) =>
                              handleOptionalFieldToggle(
                                'createCachePrice',
                                checked,
                              )
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'createCachePrice',
                          )
                        }
                        disabled={!hasValue(selectedModel.inputPrice)}
                        extraText={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'createCachePrice',
                          )
                            ? t('当前未启用，需要时再打开即可。')
                            : ''
                        }
                      />
                    </Card>

                    {timePriceCard}

                    <Card
                      bodyStyle={{ padding: 16 }}
                      style={{
                        marginBottom: 16,
                        background: 'var(--semi-color-fill-0)',
                      }}
                    >
                      <div className='mb-3'>
                        <div className='font-medium'>{t('扩展价格')}</div>
                        <div className='text-xs text-gray-500 mt-1'>
                          {t('这些价格都是可选项，不填也可以。')}
                        </div>
                      </div>
                      <PriceInput
                        label={t('图片输入价格')}
                        value={selectedModel.imagePrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('imagePrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'imagePrice',
                            )}
                            onChange={(checked) =>
                              handleOptionalFieldToggle('imagePrice', checked)
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(selectedModel, 'imagePrice')
                        }
                        disabled={!hasValue(selectedModel.inputPrice)}
                        extraText={
                          !isOptionalFieldEnabled(selectedModel, 'imagePrice')
                            ? t('当前未启用，需要时再打开即可。')
                            : ''
                        }
                      />
                      <PriceInput
                        label={t('音频输入价格')}
                        value={selectedModel.audioInputPrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('audioInputPrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'audioInputPrice',
                            )}
                            onChange={(checked) =>
                              handleOptionalFieldToggle(
                                'audioInputPrice',
                                checked,
                              )
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'audioInputPrice',
                          )
                        }
                        disabled={!hasValue(selectedModel.inputPrice)}
                        extraText={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'audioInputPrice',
                          )
                            ? t('当前未启用，需要时再打开即可。')
                            : ''
                        }
                      />
                      <PriceInput
                        label={t('音频输出价格')}
                        value={selectedModel.audioOutputPrice}
                        usdRate={effectiveUsdRate}
                        placeholder={t('输入 ¥/1M tokens')}
                        onChange={(value) =>
                          handleNumericFieldChange('audioOutputPrice', value)
                        }
                        headerAction={
                          <Switch
                            size='small'
                            checked={isOptionalFieldEnabled(
                              selectedModel,
                              'audioOutputPrice',
                            )}
                            disabled={
                              !isOptionalFieldEnabled(
                                selectedModel,
                                'audioInputPrice',
                              )
                            }
                            onChange={(checked) =>
                              handleOptionalFieldToggle(
                                'audioOutputPrice',
                                checked,
                              )
                            }
                          />
                        }
                        hidden={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'audioOutputPrice',
                          )
                        }
                        disabled={!hasValue(selectedModel.audioInputPrice)}
                        extraText={
                          !isOptionalFieldEnabled(
                            selectedModel,
                            'audioInputPrice',
                          )
                            ? t('请先开启并填写音频输入价格。')
                            : !isOptionalFieldEnabled(
                                  selectedModel,
                                  'audioOutputPrice',
                                )
                              ? t('当前未启用，需要时再打开即可。')
                              : ''
                        }
                      />
                    </Card>
                  </>
                )}

                {/* 视频计费矩阵：不是第四种计费方式，而是叠在上面按量/按次之上的
                    一层特殊场景。token 模式下上面那层管预扣、矩阵管结算，两者都要有。

                    tiered_expr 下矩阵**仍然可用**：per_call 的格子里就是终价，
                    videoPerCallPriceable 已为它放行；token 模式也可用，只要 DB 里
                    已有 ModelPrice/ModelRatio 当锚点（buildModelState 会原样带进
                    状态、serializeModel 会原样写回）。tiered 界面没有价格输入框，
                    所以只在「本来就没锚点」时才禁 token——判据是 canUseTokenVideoMatrix。
                    完整状态空间见 isVideoMatrixMissingAnchor 上方的表。 */}
                <Card
                  bodyStyle={{ padding: 16 }}
                  style={{
                    marginBottom: 16,
                    background: 'var(--semi-color-fill-0)',
                  }}
                >
                  <div className='mb-3 flex items-center justify-between gap-3'>
                    <div>
                      <div className='font-medium'>{t('视频计费矩阵')}</div>
                      <div className='text-xs text-gray-500 mt-1'>
                        {t(
                          '用于 Seedance 这类按「分辨率 × 输入是否含视频」或「分辨率 × 秒数」定价的视频模型，与供应商价目表逐格对应，可直接对抄。不开启则完全走上面的价格，行为不变。',
                        )}
                      </div>
                    </div>
                    <Switch
                      size='small'
                      checked={Boolean(selectedModel.videoMatrix)}
                      onChange={handleVideoMatrixToggle}
                    />
                  </div>

                  {selectedModel.videoMatrix ? (
                    <>
                      <Banner
                        type='info'
                        bordered
                        fullMode={false}
                        closeIcon={null}
                        style={{ marginBottom: 12 }}
                        description={
                          selectedModel.videoMatrix.mode === VIDEO_MODE_TOKEN
                            ? t(
                                '「按 Token」：实际收费 = 上游返回的 token 数 × 下表单价。上面的按量/按次价格仅用于提交时的预扣（必填），不参与最终金额。秒数已隐含在 token 里，无需也不应再按秒数缩放。',
                              )
                            : selectedModel.videoMatrix.mode ===
                                VIDEO_MODE_PER_SECOND
                              ? t(
                                  '「按秒」：下表格子里是**每秒单价**，实际收费 = 单价 × 本次时长。别把整段的总价填进去——那会按时长再乘一遍。上面的按量/按次价格不参与计算，可以留空。',
                                )
                              : t(
                                  '「按次」：下表格子里就是终价，提交时即定价、不再差额结算。上面的按量/按次价格不参与计算，可以留空。',
                                )
                        }
                      />
                      <VideoMatrixEditor
                        value={selectedModel.videoMatrix}
                        onChange={handleVideoMatrixChange}
                        allowTokenMode={canUseTokenVideoMatrix(selectedModel)}
                        resolutionOptions={videoResolutionOptionsForModel(
                          options?.VideoModelConfig,
                          selectedModel?.name,
                        )}
                        t={t}
                      />
                      <div className='mt-3 text-xs text-gray-500'>
                        {t(
                          '价格填人民币（¥/百万 tokens、¥/秒、¥/次），原样存储。留空的格子视为未配置，命中不到时该次请求回退到上面的价格路径。',
                        )}
                      </div>
                    </>
                  ) : null}
                </Card>

                <Card
                  bodyStyle={{ padding: 16 }}
                  style={{ background: 'var(--semi-color-fill-0)' }}
                >
                  <div className='font-medium mb-3'>{t('保存预览')}</div>
                  <div className='text-xs text-gray-500 mb-3'>
                    {t(
                      '下面展示这个模型保存后会写入哪些后端字段，便于和原始 JSON 编辑框保持一致。',
                    )}
                  </div>
                  <div
                    style={{
                      display: 'grid',
                      gridTemplateColumns: 'minmax(140px, 180px) 1fr',
                      gap: 8,
                    }}
                  >
                    {previewRows.map((row) => (
                      <React.Fragment key={row.key}>
                        <Text strong>{row.label}</Text>
                        <Text>{row.value}</Text>
                      </React.Fragment>
                    ))}
                  </div>
                </Card>
              </div>
            )}
          </Card>
        </div>
      </Space>

      {allowAddModel ? (
        <Modal
          title={t('添加模型')}
          visible={addVisible}
          onCancel={() => {
            setAddVisible(false);
            setNewModelName('');
          }}
          onOk={handleAddModel}
        >
          <Input
            value={newModelName}
            placeholder={t('输入模型名称，例如 gpt-4.1')}
            onChange={(value) => setNewModelName(value)}
          />
        </Modal>
      ) : null}

      <Modal
        title={t('批量应用当前模型价格')}
        visible={batchVisible}
        onCancel={() => setBatchVisible(false)}
        onOk={() => {
          if (applySelectedModelPricing()) {
            setBatchVisible(false);
          }
        }}
      >
        <div className='text-sm text-gray-600'>
          {selectedModel
            ? t(
                '将把当前编辑中的模型 {{name}} 的价格配置，批量应用到已勾选的 {{count}} 个模型。',
                {
                  name: selectedModel.name,
                  count: selectedModelNames.length,
                },
              )
            : t('请先选择一个作为模板的模型')}
        </div>
        {selectedModel ? (
          <div className='text-xs text-gray-500 mt-3'>
            {t(
              '适合同系列模型一起定价，例如把 gpt-5.1 的价格批量同步到 gpt-5.1-high、gpt-5.1-low 等模型。',
            )}
            {t('视频计费矩阵不在批量范围内，需要逐个模型配置。')}
          </div>
        ) : null}
      </Modal>
    </>
  );
}
