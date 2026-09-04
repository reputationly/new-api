import React from 'react';
import {
  Card,
  Select,
  Typography,
  Tooltip,
  InputNumber,
} from '@douyinfe/semi-ui';
import {
  Settings,
  Users,
  Sparkles,
  Ruler,
  HelpCircle,
  Shuffle,
  Images,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  makeModelOptionRenderer,
  renderGroupOption,
  selectFilter,
} from '../../helpers';
import ImageUrlInput from '../playground/ImageUrlInput';
import { useModelNotes } from '../../hooks/common/useModelNotes';
import PromptGuideTip from '../playground/PromptGuideTip';
import {
  IMAGE_MAX_EDIT_IMAGES,
  imageTierLabel,
  sizeToRatio,
} from '../../constants/imagePlayground.constants';

import {
  PLAYGROUND_BATCH_COUNTS,
  SEED_MAX,
} from '../../constants/playgroundBatch.constants';

// 比例按钮里那个小方块的长边像素。按比例缩短边，横/竖/方一眼可辨——比一串
// "16:9 / 9:16" 文字快得多，尤其横竖两版名字只差一个冒号位置。
// 20px 是「五个比例挤进一排」倒推出来的:面板 300 宽、卡片内边距 24×2,剩 252,
// 五格 + 四道 6px 缝每格只有 45px 出头,24px 的方块加上边框就顶到格子边了。
const RATIO_BOX_MAX = 20;
const ratioBox = (r) => {
  const v = sizeToRatio(r) || 1;
  return v >= 1
    ? {
        width: RATIO_BOX_MAX,
        height: Math.max(5, Math.round(RATIO_BOX_MAX / v)),
      }
    : {
        width: Math.max(5, Math.round(RATIO_BOX_MAX * v)),
        height: RATIO_BOX_MAX,
      };
};

const ImageConfigPanel = ({
  isI2I = false,
  mode = 'text2image',
  inputs,
  groups,
  models,
  availableSizes,
  // 画幅：模式决定出哪个控件（见 constants 的 getImageShapeConfig）。
  //   area  → 宽高比 + 分辨率档两个下拉，提交的像素由它俩算出来
  //   table → 尺寸下拉（值原样下发；像素与比例词都可以，后端两种都认）
  shapeMode = 'table',
  availableRatios = [],
  availableTiers = [],
  canPickI2ISize = false,
  i2iSizeOptions = [],
  i2iAspectMismatch = null,
  onInputChange,
  disabled = false,
  allowBatch = false,
  styleState,
}) => {
  const { t } = useTranslation();

  // 锁定时当前值可能已不在选项列表里，补进去以保证仍能正常显示
  const ensureOption = (options, value) => {
    if (!value) return options;
    return options.some((o) => o.value === value)
      ? options
      : [...options, { label: value, value }];
  };

  const groupOptions = ensureOption(groups || [], inputs.group);
  const modelOptions = ensureOption(models || [], inputs.model);
  // 运营给该模型在本玩法下写的备注（体验区管理里配），下拉选项与选中项下方都展示。
  const noteOf = useModelNotes('image', mode);
  const selectedNote = noteOf(inputs.model);
  const sizeOptions = ensureOption(
    (availableSizes || []).map((s) => ({ label: s, value: s })),
    inputs.size,
  );

  const usesRatio = shapeMode === 'area';
  // 刚切完模型、同步 effect 还没跑的那一帧，当前值可能不在候选里；补进去避免
  // "一个都没高亮"的闪烁。**锁定态同样要走这里**：会话记下的比例未必还在该模型当前
  // 配置的候选里（运营随时会改），补进去才显示得出来。
  const ratioChoices =
    inputs.aspectRatio && !(availableRatios || []).includes(inputs.aspectRatio)
      ? [...(availableRatios || []), inputs.aspectRatio]
      : availableRatios || [];
  // 档位下拉**只出档名**(标准/高清/超清),不出像素也不出面积基准。
  //
  // 原来写的是 `2048（2048x2048）` —— 前半段是面积基准,一个既不是宽也不是高的中间量;
  // 后半段会随比例变,四个选项的文字跟着一起跳。真实像素改由下面单独一行给出:
  // 一行代替 N 个括号,信息量一样但不跳,而且比例和档位任一变化它都跟着变。
  //
  // 只有一档时不渲染下拉(它已经生效了),避免一个只能选一项的控件 —— 那一档出多大,
  // 下面那行照样写着。
  const tierChoices =
    inputs.sizeTier && !(availableTiers || []).includes(inputs.sizeTier)
      ? [...(availableTiers || []), inputs.sizeTier]
      : availableTiers || [];
  const tierOptions = tierChoices.map((base) => ({
    label: t(imageTierLabel(base)),
    value: base,
  }));

  // 锁定态能不能照原样显示这两个控件，取决于**这条会话有没有存过比例与档位**。
  // 存了就整套按原样渲染（只是 disabled），让「生成前」和「生成后」长得一样——
  // 原先锁定态退化成一行纯文字 `1152x2048`，用户会以为设置丢了。
  //
  // 老会话只有 size、没有这两个字段（本次改造之前一直如此）。那时拿 inputs 里的残留值
  // 去高亮按钮，显示的是**上一次草稿**的选择，是假信息，比不显示更糟——所以回落到
  // 只写出 size。判据是会话自己有没有值，不是"锁没锁"。
  const showShapeControls =
    !disabled || (!!inputs.aspectRatio && !!inputs.sizeTier);
  // 未锁定时单档下拉没有意义（只能选它自己），隐藏；锁定态要显示——那时它不是选择器，
  // 而是「这张图用的哪一档」的说明。
  const showTierSelect =
    showShapeControls && tierOptions.length > (disabled ? 0 : 1);

  // 「出图 2048×1152 · 2.4MP」。百万像素是给用户一个跨比例可比的量:同一档在 1:1 与
  // 16:9 下长宽差很多,面积却基本一致,只看长宽会以为"换了比例就变小了"。
  const outputHint = (() => {
    const [w, h] = String(inputs.size || '')
      .split('x')
      .map((n) => parseInt(n, 10));
    if (!w || !h) return inputs.size || '';
    return `${w}×${h} · ${(w * h) / 1e6 >= 10 ? Math.round((w * h) / 1e6) : ((w * h) / 1e6).toFixed(1)}MP`;
  })();

  const renderImagePreview = (label, urls) => (
    <div>
      <div className='flex items-center gap-1 mb-2'>
        <Typography.Text strong className='text-sm'>
          {label}
        </Typography.Text>
      </div>
      <div className='flex flex-wrap gap-2'>
        {(urls || []).filter(Boolean).map((url, index, arr) => (
          <div key={index} className='relative'>
            <img
              src={url}
              alt={`${label}-${index + 1}`}
              className='w-20 h-20 object-cover rounded-lg border border-gray-200'
            />
            {/* 锁定态仍可续问，底图沿用这一批，所以「第 N 张」的说法照样要成立。 */}
            {arr.length > 1 && (
              <span className='absolute top-0 left-0 bg-black/60 text-white text-[10px] leading-none rounded-br-lg rounded-tl-lg px-1.5 py-1 pointer-events-none'>
                {index + 1}
              </span>
            )}
          </div>
        ))}
      </div>
    </div>
  );

  return (
    <Card
      className='h-full flex flex-col'
      bordered={false}
      bodyStyle={{
        padding: styleState?.isMobile ? '16px' : '24px',
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {/* 标题 */}
      <div className='flex items-center mb-6 flex-shrink-0'>
        <div className='w-10 h-10 rounded-full bg-gradient-to-r from-purple-500 to-pink-500 flex items-center justify-center mr-3'>
          <Settings size={20} className='text-white' />
        </div>
        <Typography.Title heading={5} className='mb-0'>
          {t('模型配置')}
        </Typography.Title>
        {/* 该玩法的提示词写作建议，悬停向右展开（没配也没内置默认时整体不渲染） */}
        <div className='ml-auto flex-shrink-0 whitespace-nowrap'>
          <PromptGuideTip category='image' tabKey={mode} />
        </div>
      </div>

      <div className='space-y-6 overflow-y-auto flex-1 pr-2'>
        {/* 分组 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Users size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('令牌分组')}
            </Typography.Text>
            <Tooltip
              content={t('仅展示包含图片生成模型的分组。')}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            placeholder={t('请选择分组')}
            name='group'
            required
            selection
            filter={selectFilter}
            autoClearSearchValue={false}
            onChange={(value) => onInputChange('group', value)}
            value={inputs.group}
            optionList={groupOptions}
            renderOptionItem={renderGroupOption}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 模型 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Sparkles size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('模型')}
            </Typography.Text>
            <Tooltip
              content={t('仅展示具备图片生成能力的模型。')}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            placeholder={t('请选择模型')}
            name='model'
            required
            selection
            filter={selectFilter}
            autoClearSearchValue={false}
            onChange={(value) => onInputChange('model', value)}
            value={inputs.model}
            optionList={modelOptions}
            renderOptionItem={makeModelOptionRenderer(noteOf)}
            emptyContent={t('当前分组下暂无图片模型')}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
          {selectedNote && (
            <Typography.Text
              type='tertiary'
              size='small'
              className='block mt-1'
            >
              {selectedNote}
            </Typography.Text>
          )}
        </div>

        {/* 底图上传（仅图生图）：新对话可编辑，锁定/历史态显示只读缩略图。 */}
        {isI2I && (
          <>
            {!disabled && (
              <ImageUrlInput
                label={t('上传图片')}
                tooltip={t('最多上传 {{count}} 张图片', {
                  count: IMAGE_MAX_EDIT_IMAGES,
                })}
                required
                maxCount={IMAGE_MAX_EDIT_IMAGES}
                // 传多张时给缩略图标序号：用户要能在提示词里说「第 2 张」，
                // 界面上就得先有「第 2 张」（见 ImageUrlInput 的 numbered 注释）。
                numbered
                imageUrls={inputs.imageUrls || []}
                onImageUrlsChange={(v) =>
                  onInputChange(
                    'imageUrls',
                    (v || []).slice(0, IMAGE_MAX_EDIT_IMAGES),
                  )
                }
                disabled={false}
              />
            )}
            {disabled &&
              (inputs.imageUrls || []).length > 0 &&
              renderImagePreview(t('图片'), inputs.imageUrls)}
          </>
        )}

        {/* 图片尺寸。文生图一直有；图生图只在运营给该模型显式配了本 tab 的 sizes
            时才出现（canPickI2ISize），每次上传底图后自动选中画幅最接近的那一档
            ——不给这个框时输出画幅由引擎默认档决定，与底图无关。 */}
        {/* 宽高比 + 分辨率档（area 模式）。运营把比例与档位都配上才出现；
            档就多一个档位下拉，两者组合算出精确像素下发——这是拿到高分辨率的唯一路，
            只发比例词的话画幅由引擎的离散表定（实测 1344x768，仅 1.03MP）。 */}
        {usesRatio && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Ruler size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {showShapeControls ? t('宽高比') : t('画幅')}
              </Typography.Text>
            </div>
            {/* 判据见上方 showShapeControls：新会话存了比例与档位，锁定态照原样渲染；
                老会话只有 size，回落到写出 size。 */}
            {!showShapeControls ? (
              <Typography.Text type='tertiary' className='text-sm'>
                {inputs.size || t('由模型决定')}
              </Typography.Text>
            ) : (
              <div
                // 一排等分:运营通常配五个比例,固定宽度 + flex-wrap 在 252px 的面板里
                // 必然折成两行(5×56 + 4×8 = 312)。按个数等分列宽后无论几个都是一行,
                // 每格宽度随个数收缩;选中态用实心蓝底,浅蓝描边在白卡片上几乎看不出来。
                className='grid gap-1.5'
                style={{
                  gridTemplateColumns: `repeat(${Math.max(ratioChoices.length, 1)}, minmax(0, 1fr))`,
                }}
              >
                {ratioChoices.map((r) => {
                  const active = r === inputs.aspectRatio;
                  return (
                    <button
                      key={r}
                      type='button'
                      disabled={disabled}
                      aria-pressed={active}
                      onClick={() => onInputChange('aspectRatio', r)}
                      // 颜色只能用 semi-color-* 这套:classic 的 tailwind.config.js 把
                      // theme.colors 整份替换成了 semi 变量,blue/gray/white 这些默认色板
                      // 在构建产物里根本不存在 —— 之前的 bg-blue-50 / border-blue-500 从来
                      // 没生效过,所以「点了没有选中效果」。
                      className={`flex min-w-0 flex-col items-center justify-center gap-1 rounded-md border px-0 py-1.5 transition-colors ${
                        active
                          ? 'border-semi-color-primary bg-semi-color-primary text-semi-color-white shadow-sm'
                          : 'border-semi-color-border text-semi-color-text-2 hover:border-semi-color-primary hover:bg-semi-color-primary-light-default'
                      } ${disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'}`}
                    >
                      <span
                        className={`block rounded-[2px] border ${
                          active
                            ? 'border-semi-color-white'
                            : 'border-semi-color-text-3'
                        }`}
                        style={ratioBox(r)}
                      />
                      <span className='text-[10px] font-medium leading-none tracking-tight'>
                        {r}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}
            {showTierSelect && (
              <div className='mt-3'>
                <div className='flex items-center gap-2 mb-2'>
                  <Typography.Text strong className='text-sm'>
                    {t('画质')}
                  </Typography.Text>
                </div>
                <Select
                  placeholder={t('请选择画质')}
                  name='sizeTier'
                  selection
                  onChange={(value) => onInputChange('sizeTier', value)}
                  value={inputs.sizeTier}
                  optionList={tierOptions}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                  className='!rounded-lg'
                />
              </div>
            )}
            {/* 把最终像素写出来:用户选的是比例和档位,真正下发的是算出来的 size,
                不显示的话"我选了 2K 到底出多大"没有任何地方能看到。 */}
            {showShapeControls && inputs.size && (
              <Typography.Text
                type='tertiary'
                size='small'
                className='block mt-1'
              >
                {t('出图 {{size}}', { size: outputHint })}
              </Typography.Text>
            )}
          </div>
        )}

        {shapeMode !== 'none' && !usesRatio && (!isI2I || canPickI2ISize) && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Ruler size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {isI2I ? t('输出尺寸') : t('图片尺寸')}
              </Typography.Text>
              {isI2I && (
                <Tooltip
                  content={t(
                    '每次上传底图后，自动选中最接近该图画幅的档位；也可手动改成其它档位',
                  )}
                >
                  <HelpCircle size={14} className='text-gray-400 cursor-help' />
                </Tooltip>
              )}
            </div>
            <Select
              placeholder={t('请选择尺寸')}
              name='size'
              selection
              onChange={(value) => onInputChange('size', value)}
              value={inputs.size}
              optionList={
                isI2I ? ensureOption(i2iSizeOptions, inputs.size) : sizeOptions
              }
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
            {/* 白名单里没有与底图同画幅的档位时说清楚后果：出图会按所选档位重新构图，
                而不是保持原样。不这么提示，用户只会看到构图被改却不知道原因。 */}
            {isI2I && i2iAspectMismatch && (
              <Typography.Text
                type='warning'
                className='text-xs block mt-1 leading-snug'
              >
                {t(
                  '底图为 {{source}}，与所选 {{selected}} 画幅不同，出图会按所选尺寸重新构图',
                  {
                    source: i2iAspectMismatch.sourceLabel,
                    selected: i2iAspectMismatch.selectedLabel,
                  },
                )}
              </Typography.Text>
            )}
          </div>
        )}

        {/* 生成张数。多张 = 同一提示词、不同 seed 的候选,供用户挑。
            **只在 web/classic 出现**:web/mobile 是独立应用,它不传 allowBatch,
            hook 里也一并按 1 走。判据是"哪个应用"而不是"屏幕多宽"——理由见
            useImageGeneration 的 allowBatch 注释。 */}
        {allowBatch && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Images size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('生成张数')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '同一提示词生成多张候选,每张用不同的随机种子,生成后可看到各自的种子。按张计费:选 3 张就是 3 次。慢模型并发多张可能被排队上限拒掉,此时会只返回成功的几张。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Select
              name='batchCount'
              value={inputs.batchCount}
              onChange={(v) => onInputChange('batchCount', v)}
              optionList={PLAYGROUND_BATCH_COUNTS.map((n) => ({
                label: t('{{n}} 张', { n }),
                value: n,
              }))}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 随机种子(seed)—— 常驻,留空为随机 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Shuffle size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('随机种子')}
            </Typography.Text>
            <Typography.Text className='text-xs text-gray-400'>
              ({t('留空为随机')})
            </Typography.Text>
          </div>
          <InputNumber
            placeholder={t('留空为随机')}
            name='seed'
            min={0}
            // 上界与 deriveSeeds / randomSeed 同一个常量:超出 32 位正整数安全区的
            // seed,引擎要么拒、要么静默截断成另一个数,两种都表现为"结果跟我给的
            // 种子对不上"。
            max={SEED_MAX}
            precision={0}
            value={
              inputs.seed === '' || inputs.seed == null
                ? undefined
                : inputs.seed
            }
            onChange={(value) =>
              onInputChange('seed', value === '' || value == null ? '' : value)
            }
            disabled={disabled}
            style={{ width: '100%' }}
            className='!rounded-lg'
          />
        </div>
      </div>
    </Card>
  );
};

export default ImageConfigPanel;
