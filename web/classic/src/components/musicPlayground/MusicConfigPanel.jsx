import React, { useRef } from 'react';
import {
  Card,
  Select,
  Typography,
  Tooltip,
  TextArea,
  Button,
  Collapse,
  Input,
  InputNumber,
} from '@douyinfe/semi-ui';
import {
  Settings,
  Users,
  Sparkles,
  Music,
  Music2,
  FileText,
  Clock,
  HelpCircle,
  Upload,
  SlidersHorizontal,
  Languages,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { renderGroupOption, selectFilter, showError } from '../../helpers';
import MediaFileInput from '../videoPlayground/MediaFileInput';
import {
  MUSIC_DURATIONS,
  MUSIC_AUDIO_UPLOAD_MAX_MB,
  MUSIC_VIDEO_UPLOAD_MAX_MB,
  MUSIC_VOCAL_LANGUAGES,
  MUSIC_DEFAULT_GUIDANCE,
  MUSIC_DEFAULT_SECONDS_TOTAL,
  MUSIC_AUDIOX_DEFAULT_GUIDANCE,
  MUSIC_SVS_LANGUAGES,
  MUSIC_SVS_CONTROLS,
  musicDefaultStepsForEngine,
} from '../../constants/musicPlayground.constants';

// 音乐模型配置面板:分组/模型(同视频/语音)+ 按 mode 的输入:
//   - acestep(cover/repaint):驱动音频上传(可试听)+ 歌词 + 时长 + BPM/演唱语言;
//   - audiox(v2a/v2m):单视频上传器(metadata.video)+ 时长(秒);
//   - soulx(svs):两个音频上传器(音色参考 + 目标曲/伴奏)+ 演唱语言/控制方式。
// 标量参数(时长/步数/贴合度/种子)按引擎显示不同默认占位。
// 对话锁定(disabled)后全部不可改,与视频/语音页一致。
const MusicConfigPanel = ({
  inputs,
  groups,
  models,
  onInputChange,
  disabled = false,
  engine = 'acestep',
  needsAudio = false,
  needsVideo = false,
  needsDualAudio = false,
  audioLabel = '',
  refAudioMaxMB = MUSIC_AUDIO_UPLOAD_MAX_MB,
  videoMaxMB = MUSIC_VIDEO_UPLOAD_MAX_MB,
  styleState,
}) => {
  const { t } = useTranslation();
  const fileInputRef = useRef(null);

  const isAceStep = engine === 'acestep';

  const ensureOption = (options, value) => {
    if (!value) return options;
    return options.some((o) => o.value === value)
      ? options
      : [...options, { label: value, value }];
  };

  const groupOptions = ensureOption(groups || [], inputs.group);
  const modelOptions = ensureOption(models || [], inputs.model);

  // 时长下拉:'' → 「自动(引擎默认)」,其余为秒数。
  const durationOptions = MUSIC_DURATIONS.map((d) =>
    d === ''
      ? { label: t('自动(引擎默认)'), value: '' }
      : { label: t('{{sec}} 秒', { sec: d }), value: d },
  );

  // 采样步数默认占位:ACE-Step 8 / AudioX 250 / SoulX 32。
  const defaultSteps = musicDefaultStepsForEngine(engine);
  const defaultGuidance = isAceStep
    ? MUSIC_DEFAULT_GUIDANCE
    : MUSIC_AUDIOX_DEFAULT_GUIDANCE;

  const handleFile = (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (refAudioMaxMB > 0 && file.size > refAudioMaxMB * 1024 * 1024) {
      showError(t('音频不能超过 {{size}} MB', { size: refAudioMaxMB }));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      onInputChange('audioData', reader.result);
      onInputChange('audioName', file.name);
    };
    reader.onerror = () => showError(t('读取音频文件失败'));
    reader.readAsDataURL(file);
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
      </div>

      <div className='space-y-6 overflow-y-auto flex-1 pr-2'>
        {/* 分组 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Users size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('分组')}
            </Typography.Text>
            <Tooltip content={t('仅展示包含音乐模型的分组。')} position='top'>
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
            <Tooltip content={t('仅展示具备该音乐能力的模型。')} position='top'>
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
            emptyContent={t('当前分组下暂无音乐模型')}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 驱动音频(ACE-Step cover=参考音频 / repaint=源音频,必选):上传后可试听 */}
        {needsAudio && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Music size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {audioLabel || t('驱动音频')}
              </Typography.Text>
              <span className='text-red-500'>*</span>
              <Tooltip
                content={t(
                  '上传作为改编/重绘依据的音频(建议 30 秒内、清晰无杂音)。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <input
              ref={fileInputRef}
              type='file'
              accept='audio/*'
              style={{ display: 'none' }}
              onChange={handleFile}
            />
            {!disabled && (
              <div className='flex items-center gap-2'>
                <Button
                  theme='outline'
                  type='tertiary'
                  size='small'
                  icon={<Upload size={14} />}
                  onClick={() => fileInputRef.current?.click()}
                >
                  {inputs.audioName ? t('重新上传') : t('选择音频文件')}
                </Button>
                {inputs.audioName && (
                  <Typography.Text
                    className='text-xs text-gray-500 truncate'
                    style={{ maxWidth: 140 }}
                  >
                    {inputs.audioName}
                  </Typography.Text>
                )}
              </div>
            )}
            {inputs.audioData && (
              <audio
                key={inputs.audioData.slice(0, 64)}
                src={inputs.audioData}
                controls
                preload='none'
                className='mt-2 w-full'
                style={{ height: 32 }}
              />
            )}
          </div>
        )}

        {/* 源视频(AudioX v2a/v2m,必选):视频条件输入 → metadata.video */}
        {needsVideo && (
          <MediaFileInput
            label={t('源视频')}
            required
            kind='video'
            value={inputs.videoData}
            maxMB={videoMaxMB}
            disabled={disabled}
            onChange={(v) => {
              onInputChange('videoData', v || '');
              if (!v) onInputChange('videoName', '');
            }}
          />
        )}

        {/* 双音频(SoulX svs,均必选):音色参考 → prompt_audio,目标曲/伴奏 → target_audio */}
        {needsDualAudio && (
          <>
            <MediaFileInput
              label={t('音色参考(人声)')}
              required
              kind='audio'
              value={inputs.promptAudioData}
              maxMB={refAudioMaxMB}
              disabled={disabled}
              onChange={(v) => {
                onInputChange('promptAudioData', v || '');
                if (!v) onInputChange('promptAudioName', '');
              }}
            />
            <MediaFileInput
              label={t('目标曲/伴奏')}
              required
              kind='audio'
              value={inputs.targetAudioData}
              maxMB={refAudioMaxMB}
              disabled={disabled}
              onChange={(v) => {
                onInputChange('targetAudioData', v || '');
                if (!v) onInputChange('targetAudioName', '');
              }}
            />
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <Languages size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('演唱语言')}
                </Typography.Text>
              </div>
              <Select
                value={inputs.language}
                onChange={(v) => onInputChange('language', v)}
                optionList={MUSIC_SVS_LANGUAGES.map((l) => ({
                  label: t(l.label),
                  value: l.value,
                }))}
                disabled={disabled}
                style={{ width: '100%' }}
                dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                className='!rounded-lg'
              />
            </div>
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <Music2 size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('控制方式')}
                </Typography.Text>
                <Tooltip
                  content={t(
                    '旋律(melody):按目标曲旋律演唱;曲谱(score):按音符曲谱演唱。',
                  )}
                  position='top'
                >
                  <HelpCircle size={14} className='text-gray-400 cursor-help' />
                </Tooltip>
              </div>
              <Select
                value={inputs.control}
                onChange={(v) => onInputChange('control', v)}
                optionList={MUSIC_SVS_CONTROLS.map((c) => ({
                  label: t(c.label),
                  value: c.value,
                }))}
                disabled={disabled}
                style={{ width: '100%' }}
                dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                className='!rounded-lg'
              />
            </div>
          </>
        )}

        {/* 歌词(仅 ACE-Step,可选):留空则由模型按描述自动生成 */}
        {isAceStep && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <FileText size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('歌词')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '可选。留空则由模型按描述自动生成歌词;填写则按此歌词演唱。支持 [verse]/[chorus]/[bridge] 等结构标签分段。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <TextArea
              placeholder={t(
                '可选,输入歌词;留空则自动生成。可用 [verse] / [chorus] / [bridge] 分段',
              )}
              value={inputs.lyrics}
              onChange={(value) => onInputChange('lyrics', value)}
              autosize={{ minRows: 5, maxRows: 14 }}
              disabled={disabled}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 时长(ACE-Step 预设下拉) */}
        {isAceStep && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Clock size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('时长')}
              </Typography.Text>
            </div>
            <Select
              name='duration'
              selection
              onChange={(value) => onInputChange('duration', value)}
              value={inputs.duration}
              optionList={durationOptions}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 时长(仅 AudioX;SoulX 歌声合成无此参数) */}
        {engine === 'audiox' && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Clock size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('时长(秒)')}
              </Typography.Text>
              <Tooltip
                content={t('生成音频的总时长(秒);留空 = 默认 {{v}}。', {
                  v: MUSIC_DEFAULT_SECONDS_TOTAL,
                })}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <InputNumber
              min={1}
              max={60}
              value={
                inputs.secondsTotal === '' ? undefined : inputs.secondsTotal
              }
              onChange={(v) => onInputChange('secondsTotal', v ?? '')}
              placeholder={t('留空 = 默认 {{v}}', {
                v: MUSIC_DEFAULT_SECONDS_TOTAL,
              })}
              disabled={disabled}
              style={{ width: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 高级参数(默认折叠,全部选填;留空即走引擎默认) */}
        <Collapse keepDOM className='!border-0'>
          <Collapse.Panel
            itemKey='advanced'
            header={
              <div className='flex items-center gap-2'>
                <SlidersHorizontal size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('高级参数')}
                </Typography.Text>
                <Typography.Text className='text-xs text-gray-400'>
                  {t('选填')}
                </Typography.Text>
              </div>
            }
          >
            <div className='space-y-4'>
              {/* 随机种子:指定后可复现;留空 = 随机 */}
              <div>
                <div className='flex items-center gap-2 mb-1'>
                  <Typography.Text className='text-xs text-gray-600'>
                    {t('随机种子 (seed)')}
                  </Typography.Text>
                  <Tooltip
                    content={t('指定后可复现同一结果;留空 = 每次随机。')}
                  >
                    <HelpCircle
                      size={13}
                      className='text-gray-400 cursor-help'
                    />
                  </Tooltip>
                </div>
                <Input
                  value={inputs.seed}
                  onChange={(v) => onInputChange('seed', v)}
                  placeholder={t('留空 = 随机')}
                  disabled={disabled}
                  className='!rounded-lg'
                />
              </div>

              {/* 演唱语言 / 速度 BPM(仅 ACE-Step) */}
              {isAceStep && (
                <>
                  <div>
                    <Typography.Text className='text-xs text-gray-600 block mb-1'>
                      {t('演唱语言')}
                    </Typography.Text>
                    <Select
                      value={inputs.vocalLanguage}
                      onChange={(v) => onInputChange('vocalLanguage', v)}
                      optionList={MUSIC_VOCAL_LANGUAGES.map((l) => ({
                        label: t(l.label),
                        value: l.value,
                      }))}
                      disabled={disabled}
                      style={{ width: '100%' }}
                      dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                      className='!rounded-lg'
                    />
                  </div>
                  <div>
                    <Typography.Text className='text-xs text-gray-600 block mb-1'>
                      {t('速度 (BPM)')}
                    </Typography.Text>
                    <InputNumber
                      min={20}
                      max={300}
                      value={inputs.bpm === '' ? undefined : inputs.bpm}
                      onChange={(v) => onInputChange('bpm', v ?? '')}
                      placeholder={t('留空 = 自动')}
                      disabled={disabled}
                      style={{ width: '100%' }}
                      className='!rounded-lg'
                    />
                  </div>
                </>
              )}

              {/* Guidance Scale */}
              <div>
                <div className='flex items-center gap-2 mb-1'>
                  <Typography.Text className='text-xs text-gray-600'>
                    {t('贴合度 (guidance)')}
                  </Typography.Text>
                  <Tooltip
                    content={t('越高越贴合描述,越低越自由;留空 = 引擎默认。')}
                  >
                    <HelpCircle
                      size={13}
                      className='text-gray-400 cursor-help'
                    />
                  </Tooltip>
                </div>
                <InputNumber
                  min={1}
                  max={20}
                  step={0.5}
                  value={
                    inputs.guidanceScale === ''
                      ? undefined
                      : inputs.guidanceScale
                  }
                  onChange={(v) => onInputChange('guidanceScale', v ?? '')}
                  placeholder={t('留空 = 默认 {{v}}', {
                    v: defaultGuidance,
                  })}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  className='!rounded-lg'
                />
              </div>

              {/* 采样步数 */}
              <div>
                <div className='flex items-center gap-2 mb-1'>
                  <Typography.Text className='text-xs text-gray-600'>
                    {t('采样步数 (steps)')}
                  </Typography.Text>
                  <Tooltip content={t('越大越精细但越慢;留空 = 引擎默认。')}>
                    <HelpCircle
                      size={13}
                      className='text-gray-400 cursor-help'
                    />
                  </Tooltip>
                </div>
                <InputNumber
                  min={1}
                  max={500}
                  value={
                    inputs.inferenceSteps === ''
                      ? undefined
                      : inputs.inferenceSteps
                  }
                  onChange={(v) => onInputChange('inferenceSteps', v ?? '')}
                  placeholder={t('留空 = 默认 {{v}}', {
                    v: defaultSteps,
                  })}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  className='!rounded-lg'
                />
              </div>
            </div>
          </Collapse.Panel>
        </Collapse>
      </div>
    </Card>
  );
};

export default MusicConfigPanel;
