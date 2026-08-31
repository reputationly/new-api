import React, { useRef, useState } from 'react';
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
  Checkbox,
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
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  makeModelOptionRenderer,
  renderGroupOption,
  selectFilter,
  showError,
} from '../../helpers';
import MediaFileInput from '../videoPlayground/MediaFileInput';
import VoiceRecorderModal from './VoiceRecorderModal';
import { useModelNotes } from '../../hooks/common/useModelNotes';
import PromptGuideTip from '../playground/PromptGuideTip';
import {
  PRESET_VOICES,
  VOICE_UPLOAD_VALUE,
  VOICE_UPLOAD_MAX_MB,
  EMOTION_PRESETS,
  AUDIO_EMOTION_SOURCES,
  AUDIO_EMOTION_SOURCE_VECTOR,
  AUDIO_EMOTION_SOURCE_AUDIO,
  AUDIO_EMOTION_SOURCE_TEXT,
  AUDIO_EMOTION_DIMENSIONS,
  AUDIO_EMOTION_DIM_MAX,
  AUDIO_EMOTION_DIM_STEP,
  AUDIO_SPEED_MIN,
  AUDIO_SPEED_MAX,
  AUDIO_SPEED_STEP,
  AUDIO_SPEED_DEFAULT,
  AUDIO_TTS25_LANGUAGES,
  makeEmptyEmotionVector,
  AUDIO_SPEAKER_PRESETS,
  speakerSampleUrl,
  AUDIO_LANGUAGES,
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
  mode = 'emotion',
  engine = 'indextts',
  needsVoice = true,
  needsEmotion = true,
  isIndexTTS25 = false,
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
  const [recorderVisible, setRecorderVisible] = useState(false);
  // 样音加载失败的 speaker(文件还没放进 public/audio-presets/speakers/)。按音色记,
  // 不是一个布尔 —— 否则第一个缺文件的音色会把其余音色的播放器一并藏掉。
  const [brokenSamples, setBrokenSamples] = useState(() => new Set());

  const ensureOption = (options, value) => {
    if (!value) return options;
    return options.some((o) => o.value === value)
      ? options
      : [...options, { label: value, value }];
  };

  const groupOptions = ensureOption(groups || [], inputs.group);
  const modelOptions = ensureOption(models || [], inputs.model);
  // 运营给该模型在本玩法下写的备注（体验区管理里配），下拉选项与选中项下方都展示。
  const noteOf = useModelNotes('audio', mode);
  const selectedNote = noteOf(inputs.model);

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
  // 老配置没有 emotionSource → 空串,走下面的旧预设路径,行为与改造前一致。
  const emotionSource = inputs.emotionSource || '';
  const emoVector = Array.isArray(inputs.emoVector)
    ? inputs.emoVector
    : makeEmptyEmotionVector();

  // 预设音色下拉:内置常用列表 + 允许自由输入(自定义 speaker 名)。
  // 选项带上官方的音色描述与母语 —— 只给一个英文名,用户分不出 Sohee 与 Serena,
  // 只能挨个合成去试。自由输入的自定义音色没有描述,渲染器按 desc 有无分支。
  const speakerOptions = ensureOption(
    AUDIO_SPEAKER_PRESETS.map((s) => ({
      label: s.label,
      value: s.value,
      desc: s.desc,
      native: s.native,
    })),
    inputs.speaker,
  );
  const selectedSpeaker = AUDIO_SPEAKER_PRESETS.find(
    (s) => s.value === inputs.speaker,
  );
  // 试听样音:静态文件,缺失时 <audio> 触发 onError → 收起播放器(见 speakerSampleBroken)。
  // 换音色要把失败标记清掉,否则一个缺文件的音色会把后面所有音色的播放器一起藏了。
  const speakerSample = selectedSpeaker ? speakerSampleUrl(inputs.speaker) : '';

  // 下拉项:音色名 + 官方描述 + 母语。自由输入的自定义音色只有名字,不占第二行。
  const renderSpeakerOption = (renderProps) => {
    const { label, value, desc, native, selected, onClick } = renderProps;
    return (
      <div
        role='option'
        aria-selected={selected}
        onClick={onClick}
        className={`px-3 py-2 cursor-pointer ${
          selected ? 'bg-blue-50' : 'hover:bg-gray-50'
        }`}
      >
        <Typography.Text className='text-sm'>{label || value}</Typography.Text>
        {desc && (
          <Typography.Text
            type='tertiary'
            size='small'
            className='block leading-tight'
          >
            {t('{{desc}} · 母语 {{native}}', { desc, native })}
          </Typography.Text>
        )}
      </div>
    );
  };

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
        {/* 该玩法的提示词写作建议，悬停向右展开（没配也没内置默认时整体不渲染） */}
        <div className='ml-auto flex-shrink-0 whitespace-nowrap'>
          <PromptGuideTip category='audio' tabKey={mode} />
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
            renderOptionItem={makeModelOptionRenderer(noteOf)}
            emptyContent={t('当前分组下暂无语音模型')}
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
                <Button
                  theme='outline'
                  type='tertiary'
                  size='small'
                  icon={<Mic size={14} />}
                  onClick={() => setRecorderVisible(true)}
                >
                  {t('录制')}
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
            <VoiceRecorderModal
              visible={recorderVisible}
              onClose={() => setRecorderVisible(false)}
              onConfirm={(dataUrl) => {
                onInputChange('voiceData', dataUrl);
                onInputChange('voiceName', t('录制音频.wav'));
              }}
            />
          </div>
        )}

        {/* 情感来源(四选一)。引擎侧 use_emo_text > emo_vector > emo_audio 是**互斥**的
            (indextts2_talker.py:832),同时给只有优先级最高的生效、其余静默失效 ——
            所以这里必须是单选,做成多个可同时填的输入框会让用户以为能叠加。 */}
        {needsEmotion && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Smile size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('情感来源')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '四选一。引擎内部互斥:同时提供多种来源时只有一种生效,所以这里只能选一个。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <RadioGroup
              type='button'
              value={emotionSource}
              onChange={(e) => onInputChange('emotionSource', e.target.value)}
              disabled={disabled}
            >
              {AUDIO_EMOTION_SOURCES.map((src) => (
                <Radio key={src.value} value={src.value}>
                  {t(src.label)}
                </Radio>
              ))}
            </RadioGroup>
            <Typography.Text className='text-xs text-gray-400 block mt-1'>
              {t(
                AUDIO_EMOTION_SOURCES.find((x) => x.value === emotionSource)
                  ?.hint || '',
              )}
            </Typography.Text>
          </div>
        )}

        {/* 八维情感(仅「手动调节」)。可混合 —— 实测悲伤0.7+低落0.4 正常出音,
            旧的 one-hot 单选是前端的自我限制,不是引擎限制。
            次序必须与引擎 _DESIRED_ORDER 一致,错位不报错只会出错情绪。 */}
        {needsEmotion && emotionSource === AUDIO_EMOTION_SOURCE_VECTOR && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Gauge size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('情感调节')}
              </Typography.Text>
              <Button
                theme='borderless'
                type='tertiary'
                size='small'
                onClick={() =>
                  onInputChange('emoVector', makeEmptyEmotionVector())
                }
                disabled={disabled}
              >
                {t('重置')}
              </Button>
            </div>
            <div className='grid grid-cols-2 gap-x-3 gap-y-1'>
              {AUDIO_EMOTION_DIMENSIONS.map((dim, i) => (
                <div key={dim.key}>
                  <div className='flex justify-between'>
                    <Typography.Text className='text-xs'>
                      {t(dim.label)}
                    </Typography.Text>
                    <Typography.Text className='text-xs text-gray-400'>
                      {Number(emoVector[i] || 0).toFixed(2)}
                    </Typography.Text>
                  </div>
                  <Slider
                    min={0}
                    max={AUDIO_EMOTION_DIM_MAX}
                    step={AUDIO_EMOTION_DIM_STEP}
                    value={emoVector[i] || 0}
                    onChange={(v) => {
                      const next = [...emoVector];
                      next[i] = v;
                      onInputChange('emoVector', next);
                    }}
                    disabled={disabled}
                  />
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 情感描述(仅「文本推断」)。留空 = 引擎按正文推断。
            标注耗时是必要的:实测首次 10.4s、之后 5.3s(基线 1.5s),QwenEmotion 推理开销,
            不提示会被当成卡死。 */}
        {needsEmotion && emotionSource === AUDIO_EMOTION_SOURCE_TEXT && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Smile size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('情感描述')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                {t('选填')}
              </Typography.Text>
            </div>
            <TextArea
              value={inputs.emoText || ''}
              onChange={(v) => onInputChange('emoText', v)}
              placeholder={t('留空则按正文推断，如「非常愤怒地质问」')}
              autosize={{ minRows: 1, maxRows: 3 }}
              disabled={disabled}
            />
            <Typography.Text className='text-xs text-gray-400 block mt-1'>
              {t('模型需额外读一遍文本判断情绪，比其他方式慢 3~4 秒。')}
            </Typography.Text>
          </div>
        )}

        {/* 情感参考音(仅「情感参考音」来源):上传一段带目标情绪的音频 → metadata.emotion_audio */}
        {needsEmotion && emotionSource === AUDIO_EMOTION_SOURCE_AUDIO && (
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

        {/* 情感预设(旧的 one-hot 路径)。仅在未选新来源时显示,保证老用户/老配置行为不变。 */}
        {needsEmotion && !emotionSource && (
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

        {/* 情感强度(emo_alpha)。八维调节与旧预设都用它,所以两种情况都显示。 */}
        {needsEmotion &&
          (emotionSource === AUDIO_EMOTION_SOURCE_VECTOR ||
            (!emotionSource && showEmotionWeight)) && (
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

        {/* ── IndexTTS-2.5 独有:语速 / 语种 / 文本归一化 ──
            按**配置声明的引擎族**判(isIndexTTS25),不按模型名 —— 前端拿对外模型名、
            后端拿渠道重定向后的上游名,靠名字判两边必然分叉。
            切回 IndexTTS-2 时整段隐藏:那三个参数在 2 上会被 talker 静默忽略,
            展示一个无效控件比不展示更糟。 */}
        {needsEmotion && isIndexTTS25 && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Gauge size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('语速')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                {Number(inputs.speed ?? AUDIO_SPEED_DEFAULT).toFixed(2)}×
              </Typography.Text>
              <Tooltip
                content={t(
                  '仅 IndexTTS-2.5 支持。范围 0.5~2.0，超出会被引擎拒绝。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Slider
              min={AUDIO_SPEED_MIN}
              max={AUDIO_SPEED_MAX}
              step={AUDIO_SPEED_STEP}
              value={inputs.speed ?? AUDIO_SPEED_DEFAULT}
              onChange={(value) => onInputChange('speed', value)}
              disabled={disabled}
            />
          </div>
        )}

        {needsEmotion && isIndexTTS25 && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Typography.Text strong className='text-sm'>
                {t('语种')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '不选则按中文处理。引擎共支持 106 个语种，这里只列常用的，API 直连可传任意合法值。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Select
              name='lang'
              selection
              value={inputs.lang || ''}
              onChange={(value) => onInputChange('lang', value)}
              optionList={AUDIO_TTS25_LANGUAGES.map((l) => ({
                label: t(l.label),
                value: l.value,
              }))}
              disabled={disabled}
              style={{ width: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {needsEmotion && isIndexTTS25 && (
          <div>
            <Checkbox
              checked={inputs.textNormalization !== false}
              onChange={(e) =>
                onInputChange('textNormalization', e.target.checked)
              }
              disabled={disabled}
            >
              <Typography.Text className='text-sm'>
                {t('文本归一化')}
              </Typography.Text>
            </Checkbox>
            <Typography.Text className='text-xs text-gray-400 block mt-1'>
              {t(
                '把数字、日期转成读法（1580 元 → 一千五百八十元）。关掉则按原文读。',
              )}
            </Typography.Text>
          </div>
        )}

        {/* 双人对话(MOSS-TTSD)的双参考音在下方 needsDualRef 区块;语音融合只做预设音色,
            不再有上传克隆/参考文本入口(CustomVoice checkpoint 不支持克隆)。 */}

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
              renderOptionItem={renderSpeakerOption}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
            {/* 选中项的描述:下拉收起后 Select 只显示音色名,描述看不到了 */}
            {selectedSpeaker && (
              <Typography.Text
                type='tertiary'
                size='small'
                className='block mt-1'
              >
                {t('{{desc}} · 母语 {{native}}', {
                  desc: selectedSpeaker.desc,
                  native: selectedSpeaker.native,
                })}
              </Typography.Text>
            )}
            {/* 试听:静态样音,与情感合成的预置音色同一套路。样音文件还没放进
                public/audio-presets/speakers/ 时 onError 收起播放器,不留空壳。 */}
            {speakerSample && !brokenSamples.has(inputs.speaker) && (
              <audio
                key={speakerSample}
                src={speakerSample}
                controls
                preload='none'
                className='mt-2 w-full'
                style={{ height: 32 }}
                onError={() =>
                  setBrokenSamples((prev) => {
                    const next = new Set(prev);
                    next.add(inputs.speaker);
                    return next;
                  })
                }
              />
            )}
          </div>
        )}

        {/* 口音(语音融合):默认自动;方言(北京话/四川话)仅对中文文本生效 → metadata.language */}
        {needsLanguage && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Languages size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('口音')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '默认自动:引擎按文本语言发音(TTS 不翻译)。方言(北京话/四川话)仅对中文文本生效。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
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
