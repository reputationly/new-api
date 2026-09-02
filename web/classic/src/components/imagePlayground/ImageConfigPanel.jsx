import React from 'react';
import {
  Card,
  Select,
  Typography,
  Tooltip,
  InputNumber,
  Switch,
} from '@douyinfe/semi-ui';
import {
  Settings,
  Users,
  Sparkles,
  Ruler,
  HelpCircle,
  Shuffle,
  Gauge,
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
  DEFAULT_IMAGE_SIZE_ALIGN,
  computeImageSize,
  sizeToRatio,
} from '../../constants/imagePlayground.constants';

import {
  PLAYGROUND_BATCH_COUNTS,
  SEED_MAX,
} from '../../constants/playgroundBatch.constants';

// 比例按钮里那个小方块的长边像素。按比例缩短边，横/竖/方一眼可辨——比一串
// "16:9 / 9:16" 文字快得多，尤其横竖两版名字只差一个冒号位置。
const RATIO_BOX_MAX = 24;
const ratioBox = (r) => {
  const v = sizeToRatio(r) || 1;
  return v >= 1
    ? {
        width: RATIO_BOX_MAX,
        height: Math.max(6, Math.round(RATIO_BOX_MAX / v)),
      }
    : {
        width: Math.max(6, Math.round(RATIO_BOX_MAX * v)),
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
  // 对齐粒度按模型走（实测 U1.5 是 32、Qwen-Image 官方表是 16）。用错会让下拉里
  // 写的像素和实际出图对不上，所以这里必须拿模型的值，不能用默认值凑。
  sizeAlign = DEFAULT_IMAGE_SIZE_ALIGN,
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
  // "一个都没高亮"的闪烁。**锁定态不走这里**——那时显示的是真实产出值，见下方 disabled 分支。
  const ratioChoices =
    inputs.aspectRatio && !(availableRatios || []).includes(inputs.aspectRatio)
      ? [...(availableRatios || []), inputs.aspectRatio]
      : availableRatios || [];
  // 档位标签直接写出算出来的像素:运营配的是"面积基准",用户关心的是"出多大的图"。
  // 只有一档时不渲染下拉(它已经生效了),避免一个只能选一项的控件。
  const tierOptions = (availableTiers || []).map((base) => {
    const px = computeImageSize(inputs.aspectRatio, base, sizeAlign);
    return { label: px ? `${base}（${px}）` : String(base), value: base };
  });

  const renderImagePreview = (label, urls) => (
    <div>
      <div className='flex items-center gap-1 mb-2'>
        <Typography.Text strong className='text-sm'>
          {label}
        </Typography.Text>
      </div>
      <div className='flex flex-wrap gap-2'>
        {(urls || []).filter(Boolean).map((url, index) => (
          <img
            key={index}
            src={url}
            alt={`${label}-${index + 1}`}
            className='w-20 h-20 object-cover rounded-lg border border-gray-200'
          />
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
                label={t('上传底图')}
                tooltip={t('最多上传 {{count}} 张底图', {
                  count: IMAGE_MAX_EDIT_IMAGES,
                })}
                required
                maxCount={IMAGE_MAX_EDIT_IMAGES}
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
              renderImagePreview(t('底图'), inputs.imageUrls)}
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
                {disabled ? t('画幅') : t('宽高比')}
              </Typography.Text>
            </div>
            {/* 锁定态（打开历史会话）只显示真正产出这张图的值。
                会话里只存了 size，比例与档位从来没存过（改造前的老会话更不可能有），
                拿 inputs 里的残留值去高亮按钮，显示的是**上一次草稿**的选择——
                那是假信息，比不显示更糟。直接写出 size 即可——它是从会话恢复的，
                是唯一的事实，不需要反推比例或档位。 */}
            {disabled ? (
              <Typography.Text type='tertiary' className='text-sm'>
                {inputs.size || t('由模型决定')}
              </Typography.Text>
            ) : (
              <div className='flex flex-wrap gap-2'>
                {ratioChoices.map((r) => {
                  const active = r === inputs.aspectRatio;
                  return (
                    <button
                      key={r}
                      type='button'
                      disabled={disabled}
                      aria-pressed={active}
                      onClick={() => onInputChange('aspectRatio', r)}
                      className={`flex w-14 flex-col items-center justify-center gap-1 rounded-lg border py-2 transition-colors ${
                        active
                          ? 'border-blue-500 bg-blue-50'
                          : 'border-gray-200 hover:border-gray-300'
                      } ${disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'}`}
                    >
                      <span
                        className={`block rounded-sm border ${
                          active ? 'border-blue-500' : 'border-gray-400'
                        }`}
                        style={ratioBox(r)}
                      />
                      <span
                        className={`text-[11px] leading-none ${
                          active ? 'text-blue-600' : 'text-gray-500'
                        }`}
                      >
                        {r}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}
            {!disabled && tierOptions.length > 1 && (
              <div className='mt-3'>
                <div className='flex items-center gap-2 mb-2'>
                  <Typography.Text strong className='text-sm'>
                    {t('分辨率')}
                  </Typography.Text>
                </div>
                <Select
                  placeholder={t('请选择分辨率')}
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
            {!disabled && inputs.size && (
              <Typography.Text
                type='tertiary'
                size='small'
                className='block mt-1'
              >
                {t('出图 {{size}}', { size: inputs.size })}
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

        {/* 提示词智能优化：默认关闭。 */}
        <div>
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <Gauge size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('提示词智能优化')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '开启后会自动丰富和优化提示词，适合只写了主题或简短创意的情况。如果提示词已经很详细，或对文字、数量、位置和版式有严格要求，建议关闭。部分模型（如 hunyuan-image-3）开启后会先思考再改写提示词，生成时长明显增加（约 2.8 倍）。',
                )}
                position='top'
                style={{ maxWidth: 320 }}
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Switch
              checked={!!inputs.qualityMode}
              onChange={(checked) => onInputChange('qualityMode', checked)}
              checkedText={t('开')}
              uncheckedText={t('关')}
              size='small'
              disabled={disabled}
            />
          </div>
          <Typography.Text className='text-xs text-gray-400'>
            {t('简短描述建议开启；详细文案或严格版式建议关闭')}
          </Typography.Text>
        </div>

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
