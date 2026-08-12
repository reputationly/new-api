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
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  makeModelOptionRenderer,
  renderGroupOption,
  selectFilter,
} from '../../helpers';
import ImageUrlInput from '../playground/ImageUrlInput';
import { useModelNotes } from '../../hooks/common/useModelNotes';
import { IMAGE_MAX_EDIT_IMAGES } from '../../constants/imagePlayground.constants';

const ImageConfigPanel = ({
  isI2I = false,
  mode = 'text2image',
  inputs,
  groups,
  models,
  availableSizes,
  onInputChange,
  disabled = false,
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
      </div>

      <div className='space-y-6 overflow-y-auto flex-1 pr-2'>
        {/* 分组 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Users size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('分组')}
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

        {/* 图片尺寸（图生图跟随参考图，不显示、不下发） */}
        {!isI2I && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Ruler size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('图片尺寸')}
              </Typography.Text>
            </div>
            <Select
              placeholder={t('请选择尺寸')}
              name='size'
              selection
              onChange={(value) => onInputChange('size', value)}
              value={inputs.size}
              optionList={sizeOptions}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
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
