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

import React, { useCallback, useContext, useMemo, useState } from 'react';
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
import {
  PAGE_SIZE,
  PRICE_SUFFIX,
  VIDEO_MODE_TOKEN,
  VIDEO_MODE_PER_SECOND,
  buildSummaryText,
  canUseTokenVideoMatrix,
  videoResolutionOptionsForModel,
  formatDisplayPrice,
  hasValue,
  normalizeRate,
  useModelPricingEditorState,
} from '../hooks/useModelPricingEditorState';
import { useIsMobile } from '../../../../hooks/common/useIsMobile';
import { StatusContext } from '../../../../context/Status';
import TieredPricingEditor from './TieredPricingEditor';
import VideoMatrixEditor from './VideoMatrixEditor';

const { Text } = Typography;
const EMPTY_CANDIDATE_MODEL_NAMES = [];

// 显示固定 2 位（¥4.00），编辑时才展开原始精度串；失焦不回写圆整值，
// 故 model state 始终保持高精度，序列化不受 2 位显示影响（见设计 §6.1.1）。
const PriceInput = ({
  label,
  value,
  placeholder,
  onChange,
  suffix = PRICE_SUFFIX,
  disabled = false,
  extraText = '',
  headerAction = null,
  hidden = false,
}) => {
  const [focused, setFocused] = useState(false);
  const [draft, setDraft] = useState('');
  const displayValue = focused ? draft : formatDisplayPrice(value);
  return (
    <div style={{ marginBottom: 16 }}>
      <div className='mb-1 font-medium text-gray-700 flex items-center justify-between gap-3'>
        <span>{label}</span>
        {headerAction}
      </div>
      {!hidden ? (
        <Input
          value={displayValue}
          placeholder={placeholder}
          onFocus={() => {
            // 编辑起点用原始高精度串，避免廉价模型（如 ¥0.0146）被 2 位显示圆整后丢精度。
            setDraft(value ?? '');
            setFocused(true);
          }}
          onBlur={() => setFocused(false)}
          onChange={(v) => {
            setDraft(v);
            onChange(v);
          }}
          suffix={suffix}
          disabled={disabled}
        />
      ) : null}
      {extraText ? (
        <div className='mt-1 text-xs text-gray-500'>{extraText}</div>
      ) : null}
    </div>
  );
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
}) {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const [statusState] = useContext(StatusContext);
  // 优先用页面级、随保存刷新的 options.USDExchangeRate；StatusContext 仅 app 启动加载一次，
  // 改汇率后同会话不刷新会折算错（写错计费倍率），故仅作兜底。
  const rate = normalizeRate(
    options?.USDExchangeRate ?? statusState?.status?.usd_exchange_rate,
  );
  const [addVisible, setAddVisible] = useState(false);
  const [batchVisible, setBatchVisible] = useState(false);
  const [newModelName, setNewModelName] = useState('');

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
    rate,
  });

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
            '价格单位为人民币（¥），按运营设置的「美元兑人民币汇率」{{rate}} 自动折算为后端倍率存储；价格显示保留 2 位小数，存储仍为高精度，计费不受影响。（注：表达式/阶梯计费仍按美元 $ 计价，不受此折算影响。）',
            { rate },
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
            onClick={handleSubmit}
            style={isMobile ? { width: '100%' } : undefined}
          >
            {t('应用更改')}
          </Button>
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
                  <PriceInput
                    label={t('固定价格')}
                    value={selectedModel.fixedPrice}
                    placeholder={t('输入每次调用价格')}
                    suffix={t('¥/次')}
                    onChange={(value) =>
                      handleNumericFieldChange('fixedPrice', value)
                    }
                    extraText={t('适合 MJ / 任务类等按次收费模型。')}
                  />
                ) : selectedModel.billingMode === 'tiered_expr' ? (
                  <TieredPricingEditor
                    model={selectedModel}
                    onExprChange={handleBillingExprChange}
                    requestRuleExpr={selectedModel.requestRuleExpr}
                    onRequestRuleExprChange={handleRequestRuleExprChange}
                    t={t}
                  />
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
                        label={t('缓存输入价格')}
                        value={selectedModel.cachePrice}
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
                          '价格填人民币，按运营设置的汇率 {{rate}} 折算为美元存储。留空的格子视为未配置，命中不到时该次请求回退到上面的价格路径。',
                          { rate },
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
