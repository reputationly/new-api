import React, { useRef } from 'react';
import {
  Card,
  Select,
  Typography,
  Tooltip,
  Slider,
  Button,
  TextArea,
  RadioGroup,
  Radio,
  Switch,
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
  Languages,
  Wand2,
  FileText,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { renderGroupOption, selectFilter, showError } from '../../helpers';
import MediaFileInput from '../videoPlayground/MediaFileInput';
import {
  PRESET_VOICES,
  VOICE_UPLOAD_VALUE,
  VOICE_UPLOAD_MAX_MB,
  EMOTION_PRESETS,
  AUDIO_SPEAKER_PRESETS,
  AUDIO_LANGUAGES,
  AUDIO_VOICE_SOURCE_OPTIONS,
} from '../../constants/audioPlayground.constants';

// 语音合成配置面板:分组/模型(同视频)+ 按 mode 的输入:
//   - emotion(情感合成):参考音色(预置下拉+上传,可试听)+ 情感参考音 + 情感预设/强度;
//   - synthesis(语音合成):音色来源 toggle(上传克隆 → 参考音+可选参考文本 | 预设音色 → 音色
//     下拉)+ 语言下拉(可选);
//   - dialogue(双人对话):说话人1/2 双参考音上传;
//   - design(声音设计):声线描述文本。
// 语音合成里 needsRefAudio/needsRefText/needsSpeaker 由 hook 按音色来源 toggle 派生下发,
// 面板据此展示对应输入项(上传克隆 vs 预设音色互斥)。对话锁定(disabled)后全部不可改。
const AudioConfigPanel = ({
  inputs,
  groups,
  models,
  onInputChange,
  disabled = false,
  engine = 'indextts',
  needsVoice = true,
  needsEmotion = true,
  needsVoiceSource = false,
  needsRefAudio = false,
  refAudioRequired = false,
  needsDualRef = false,
  needsSpeaker = false,
  needsLanguage = false,
  needsRefText = false,
  needsInstructions = false,
  instructionsRequired = false,
  refAudioMaxMB = VOICE_UPLOAD_MAX_MB,
  styleState,
}) => {
  const { t } = useTranslation();
  const fileInputRef = useRef(null);
  const emotionAudioRef = useRef(null);

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
      showError(t('参考音频不能超过 {{size}} MB', { size: refAudioMaxMB }));
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

  const handleEmotionAudioFile = (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (refAudioMaxMB > 0 && file.size > refAudioMaxMB * 1024 * 1024) {
      showError(t('参考音频不能超过 {{size}} MB', { size: refAudioMaxMB }));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      onInputChange('emotionAudioData', reader.result);
      onInputChange('emotionAudioName', file.name);
    };
    reader.onerror = () => showError(t('读取音频文件失败'));
    reader.readAsDataURL(file);
  };

  const showEmotionWeight = !!inputs.emotion;

  // 预设音色下拉:内置常用列表 + 允许自由输入(自定义 speaker 名)。
  const speakerOptions = ensureOption(
    AUDIO_SPEAKER_PRESETS.map((s) => ({ label: s.label, value: s.value })),
    inputs.speaker,
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
              content={t('仅展示包含该语音能力模型的分组。')}
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
            <Tooltip content={t('仅展示具备该语音能力的模型。')} position='top'>
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

        {/* 参考音色(情感合成,IndexTTS zero-shot 克隆源,必选):预置或上传,可试听 */}
        {needsVoice && (
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
                  {inputs.voiceName ? t('重新上传') : t('选择音频文件')}
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
        )}

        {/* 情感参考音(情感合成,可选):上传一段带目标情绪的音频 → metadata.emotion_audio */}
        {needsEmotion && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Mic size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('情感参考音')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                {t('选填')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '可选。上传一段带目标情绪的音频,合成语音将迁移其情感表现(与情感预设二选一即可)。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <input
              ref={emotionAudioRef}
              type='file'
              accept='audio/*'
              style={{ display: 'none' }}
              onChange={handleEmotionAudioFile}
            />
            {!disabled && (
              <div className='flex items-center gap-2'>
                <Button
                  theme='outline'
                  type='tertiary'
                  size='small'
                  icon={<Upload size={14} />}
                  onClick={() => emotionAudioRef.current?.click()}
                >
                  {inputs.emotionAudioName ? t('重新上传') : t('选择音频文件')}
                </Button>
                {inputs.emotionAudioName && (
                  <Typography.Text
                    className='text-xs text-gray-500 truncate'
                    style={{ maxWidth: 140 }}
                  >
                    {inputs.emotionAudioName}
                  </Typography.Text>
                )}
              </div>
            )}
            {inputs.emotionAudioData && (
              <audio
                key={inputs.emotionAudioData.slice(0, 64)}
                src={inputs.emotionAudioData}
                controls
                preload='none'
                className='mt-2 w-full'
                style={{ height: 32 }}
              />
            )}
          </div>
        )}

        {/* 情感预设(情感合成,one-hot 情感向量;默认跟随参考音色) */}
        {needsEmotion && (
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
        )}

        {/* 情感强度(emo_alpha;仅选了情绪时展示) */}
        {needsEmotion && showEmotionWeight && (
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

        {/* 音色来源(语音合成):上传克隆 | 预设音色。切换驱动下面 ref_audio/speaker 二选一。 */}
        {needsVoiceSource && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Mic size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('音色来源')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '上传克隆:上传一段参考音克隆其音色(适用所有语音合成模型)。预设音色:选用模型内置音色(Qwen3-TTS 等)。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <RadioGroup
              type='button'
              value={inputs.voiceSource}
              onChange={(e) => onInputChange('voiceSource', e.target.value)}
              disabled={disabled}
            >
              {AUDIO_VOICE_SOURCE_OPTIONS.map((o) => (
                <Radio key={o.value} value={o.value}>
                  {t(o.label)}
                </Radio>
              ))}
            </RadioGroup>
          </div>
        )}

        {/* 参考音(上传克隆源必选;dialogue 走下方双上传) */}
        {needsRefAudio && !needsDualRef && (
          <MediaFileInput
            label={needsRefText ? t('参考音(克隆源)') : t('参考音')}
            required={refAudioRequired}
            kind='audio'
            value={inputs.refAudioData}
            maxMB={refAudioMaxMB}
            disabled={disabled}
            onChange={(v) => {
              onInputChange('refAudioData', v || '');
              if (!v) onInputChange('refAudioName', '');
            }}
          />
        )}

        {/* 参考文本(声音克隆):参考音对应的文字稿。未开「仅用音色向量」时必填(引擎克隆需
            参考音转录做 ICL,否则上游报 ref_text 必填);开启则改为只克隆音色、免文本 →
            metadata.ref_text / metadata.x_vector_only_mode */}
        {needsRefText && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <FileText size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('参考文本')}
              </Typography.Text>
              {!inputs.xVectorOnlyMode && (
                <span className='text-red-500'>*</span>
              )}
              <Tooltip
                content={t(
                  '参考音对应的文字稿。声音克隆需要它做上下文学习;若不便提供,可开启下方「仅用音色向量」改为只克隆音色。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <div className='flex items-center gap-2 mb-2'>
              <Switch
                size='small'
                checked={!!inputs.xVectorOnlyMode}
                onChange={(v) => onInputChange('xVectorOnlyMode', v)}
                disabled={disabled}
              />
              <Typography.Text className='text-xs text-gray-500'>
                {t('仅用音色向量(免参考文本)')}
              </Typography.Text>
            </div>
            <TextArea
              placeholder={
                inputs.xVectorOnlyMode
                  ? t('已开启仅用音色向量,无需参考文本')
                  : t('请输入参考音对应的文字稿')
              }
              value={inputs.refText}
              onChange={(value) => onInputChange('refText', value)}
              autosize={{ minRows: 2, maxRows: 5 }}
              disabled={disabled || inputs.xVectorOnlyMode}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 双人对话(MOSS-TTSD):说话人1 → ref_audio,说话人2 → ref_audio_2,均必选 */}
        {needsDualRef && (
          <>
            <MediaFileInput
              label={t('说话人1 参考音')}
              required
              kind='audio'
              value={inputs.refAudioData}
              maxMB={refAudioMaxMB}
              disabled={disabled}
              onChange={(v) => {
                onInputChange('refAudioData', v || '');
                if (!v) onInputChange('refAudioName', '');
              }}
            />
            <MediaFileInput
              label={t('说话人2 参考音')}
              required
              kind='audio'
              value={inputs.refAudio2Data}
              maxMB={refAudioMaxMB}
              disabled={disabled}
              onChange={(v) => {
                onInputChange('refAudio2Data', v || '');
                if (!v) onInputChange('refAudio2Name', '');
              }}
            />
          </>
        )}

        {/* 预设音色(语音合成 → 音色来源=预设音色):下拉常用音色 + 自由输入 → metadata.speaker */}
        {needsSpeaker && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Mic size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('音色')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '选择内置音色或直接输入自定义音色名(随请求透传给引擎)。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Select
              filter
              allowCreate
              selection
              placeholder={t('选择或输入音色名')}
              onChange={(value) => onInputChange('speaker', value)}
              value={inputs.speaker}
              optionList={speakerOptions}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 多语言/方言(Qwen3-TTS/CosyVoice3):语言下拉 → metadata.language(标量透传) */}
        {needsLanguage && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Languages size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('语言/方言')}
              </Typography.Text>
            </div>
            <Select
              selection
              onChange={(value) => onInputChange('language', value)}
              value={inputs.language}
              optionList={AUDIO_LANGUAGES.map((l) => ({
                label: t(l.label),
                value: l.value,
              }))}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 声线描述(声音设计=必填)→ metadata.instructions */}
        {needsInstructions && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Wand2 size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('声线描述')}
              </Typography.Text>
              {instructionsRequired && <span className='text-red-500'>*</span>}
              <Tooltip
                content={t(
                  '用自然语言描述目标声线(如「温柔知性的中年女性,声音低沉」),引擎据此凭空设计音色。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <TextArea
              placeholder={t(
                '描述目标声线,如:活泼开朗的少年,声音清亮,语速偏快',
              )}
              value={inputs.instructions}
              onChange={(value) => onInputChange('instructions', value)}
              autosize={{ minRows: 3, maxRows: 6 }}
              disabled={disabled}
              className='!rounded-lg'
            />
          </div>
        )}
      </div>
    </Card>
  );
};

export default AudioConfigPanel;
