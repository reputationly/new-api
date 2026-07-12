import React, { useRef } from 'react';
import {
  Card,
  Select,
  Typography,
  Tooltip,
  Slider,
  Button,
} from '@douyinfe/semi-ui';
import {
  Settings,
  Users,
  Sparkles,
  Mic,
  Smile,
  Gauge,
  HelpCircle,
  Upload,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { renderGroupOption, selectFilter, showError } from '../../helpers';
import {
  PRESET_VOICES,
  VOICE_UPLOAD_VALUE,
  VOICE_UPLOAD_MAX_MB,
  EMOTION_PRESETS,
} from '../../constants/audioPlayground.constants';

// 语音合成配置面板:分组/模型(同视频)+ 参考音色(预置下拉+上传,可试听)
// + 情感预设 + 情感强度。对话锁定(disabled)后全部不可改,与视频页一致。
const AudioConfigPanel = ({
  inputs,
  groups,
  models,
  onInputChange,
  disabled = false,
  refAudioMaxMB = VOICE_UPLOAD_MAX_MB,
  styleState,
}) => {
  const { t } = useTranslation();
  const fileInputRef = useRef(null);

  const ensureOption = (options, value) => {
    if (!value) return options;
    return options.some((o) => o.value === value)
      ? options
      : [...options, { label: value, value }];
  };

  const groupOptions = ensureOption(groups || [], inputs.group);
  const modelOptions = ensureOption(models || [], inputs.model);

  const voiceOptions = [
    ...PRESET_VOICES.map((v) => ({ label: t(v.label), value: v.id })),
    { label: t('上传自定义音频…'), value: VOICE_UPLOAD_VALUE },
  ];

  const isUpload = inputs.voicePreset === VOICE_UPLOAD_VALUE;
  const presetUrl = !isUpload
    ? PRESET_VOICES.find((v) => v.id === inputs.voicePreset)?.url || ''
    : '';
  // 试听源:预置用静态 URL(浏览器缓存),上传用 data-url。
  const auditionSrc = isUpload ? inputs.voiceData || '' : presetUrl;

  const handleFile = (e) => {
    const file = e.target.files?.[0];
    // 允许再次选同一文件触发 onChange
    e.target.value = '';
    if (!file) return;
    if (refAudioMaxMB > 0 && file.size > refAudioMaxMB * 1024 * 1024) {
      showError(
        t('参考音频不能超过 {{size}} MB', { size: refAudioMaxMB }),
      );
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      onInputChange('voiceData', reader.result);
      onInputChange('voiceName', file.name);
    };
    reader.onerror = () => showError(t('读取音频文件失败'));
    reader.readAsDataURL(file);
  };

  const showEmotionWeight = !!inputs.emotion;

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
      <div className='flex items-center mb-6 flex-shrink-0'>
        <div className='w-10 h-10 rounded-full bg-gradient-to-r from-blue-500 to-cyan-500 flex items-center justify-center mr-3'>
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
              content={t('仅展示包含语音合成模型的分组。')}
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
              content={t('仅展示具备语音合成能力的模型。')}
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
            emptyContent={t('当前分组下暂无语音模型')}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 参考音色(zero-shot 克隆源,必选):预置或上传,可试听 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Mic size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('参考音色')}
            </Typography.Text>
            <span className='text-red-500'>*</span>
            <Tooltip
              content={t(
                '合成语音将克隆该参考音的音色。可选预置音色或上传 5-10 秒干净人声。',
              )}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            placeholder={t('请选择参考音色')}
            name='voicePreset'
            selection
            onChange={(value) => {
              onInputChange('voicePreset', value);
              if (value === VOICE_UPLOAD_VALUE) {
                // 切到上传即弹文件选择;已有上传内容则保留供替换
                if (!inputs.voiceData) fileInputRef.current?.click();
              }
            }}
            value={inputs.voicePreset}
            optionList={voiceOptions}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
          <input
            ref={fileInputRef}
            type='file'
            accept='audio/*'
            style={{ display: 'none' }}
            onChange={handleFile}
          />
          {isUpload && !disabled && (
            <div className='flex items-center gap-2 mt-2'>
              <Button
                theme='outline'
                type='tertiary'
                size='small'
                icon={<Upload size={14} />}
                onClick={() => fileInputRef.current?.click()}
              >
                {inputs.voiceName
                  ? t('重新上传')
                  : t('选择音频文件')}
              </Button>
              {inputs.voiceName && (
                <Typography.Text
                  className='text-xs text-gray-500 truncate'
                  style={{ maxWidth: 140 }}
                >
                  {inputs.voiceName}
                </Typography.Text>
              )}
            </div>
          )}
          {auditionSrc && (
            <audio
              key={auditionSrc.slice(0, 64)}
              src={auditionSrc}
              controls
              preload='none'
              className='mt-2 w-full'
              style={{ height: 32 }}
            />
          )}
        </div>

        {/* 情感预设(one-hot 情感向量;默认跟随参考音色) */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Smile size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('情感')}
            </Typography.Text>
            <Tooltip
              content={t(
                '默认跟随参考音色的情感;选择情绪后按下方强度合成对应情感。',
              )}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            name='emotion'
            selection
            onChange={(value) => onInputChange('emotion', value)}
            value={inputs.emotion}
            optionList={EMOTION_PRESETS.map((e) => ({
              label: t(e.label),
              value: e.value,
            }))}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 情感强度(emo_alpha;仅选了情绪时展示) */}
        {showEmotionWeight && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Gauge size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('情感强度')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                {Number(inputs.emoWeight).toFixed(2)}
              </Typography.Text>
            </div>
            <Slider
              min={0}
              max={1}
              step={0.05}
              value={inputs.emoWeight}
              onChange={(value) => onInputChange('emoWeight', value)}
              disabled={disabled}
            />
          </div>
        )}
      </div>
    </Card>
  );
};

export default AudioConfigPanel;
