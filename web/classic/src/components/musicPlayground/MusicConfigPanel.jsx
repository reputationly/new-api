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
import {
  makeModelOptionRenderer,
  renderGroupOption,
  selectFilter,
  showError,
} from '../../helpers';
import MediaFileInput from '../videoPlayground/MediaFileInput';
import { useModelNotes } from '../../hooks/common/useModelNotes';
import PromptGuideTip from '../playground/PromptGuideTip';
import {
  MUSIC_DURATIONS,
  MUSIC_AUDIO_UPLOAD_MAX_MB,
  MUSIC_VIDEO_UPLOAD_MAX_MB,
  MUSIC_VOCAL_LANGUAGES,
  MUSIC_DEFAULT_SECONDS_TOTAL,
  MUSIC_SVS_LANGUAGES,
  MUSIC_SVS_CONTROLS,
  MUSIC_DEFAULT_COVER_STRENGTH,
  MUSIC_REPAINT_MODES,
  MUSIC_DEFAULT_REPAINT_STRENGTH,
  MUSIC_REPAINT_MIN_SEC,
  MUSIC_REPAINT_MAX_SEC,
  musicDefaultStepsForEngine,
  musicDefaultGuidanceForEngine,
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
  mode = 't2m',
  engine = 'acestep',
  needsAudio = false,
  needsVideo = false,
  needsDualAudio = false,
  showTranslation = false,
  showAssistModel = false,
  translationGroups = [],
  translationModels = [],
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
  // 运营给该模型在本玩法下写的备注（体验区管理里配），下拉选项与选中项下方都展示。
  const noteOf = useModelNotes('music', mode);
  const selectedNote = noteOf(inputs.model);
  const translationGroupOptions = ensureOption(
    translationGroups || [],
    inputs.translationGroup,
  );
  const translationModelOptions = ensureOption(
    translationModels || [],
    inputs.translationModel,
  );

  // 时长下拉:'' → 「自动(引擎默认)」,其余为秒数。
  const durationOptions = MUSIC_DURATIONS.map((d) =>
    d === ''
      ? { label: t('自动(引擎默认)'), value: '' }
      : { label: t('{{sec}} 秒', { sec: d }), value: d },
  );

  // 占位默认按引擎:采样步数 ACE-Step 8 / AudioX 250 / SoulX 32;
  // guidance AudioX·ACE-Step 7 / SoulX 3(与 deploy-config 一致,所见即所发)。
  const defaultSteps = musicDefaultStepsForEngine(engine);
  const defaultGuidance = musicDefaultGuidanceForEngine(engine);

  // 重绘区间越界提示。两个都留空 = 全曲重绘(引擎默认),不提示。
  const repaintRangeWarning = (() => {
    const a = parseFloat(inputs.repaintStart);
    const b = parseFloat(inputs.repaintEnd);
    if (!Number.isFinite(a) || !Number.isFinite(b)) return '';
    if (b <= a) return t('重绘区间的结束时间要大于开始时间');
    const span = b - a;
    if (span < MUSIC_REPAINT_MIN_SEC || span > MUSIC_REPAINT_MAX_SEC) {
      return t('重绘区间建议在 {{min}}~{{max}} 秒之间(当前 {{cur}} 秒)', {
        min: MUSIC_REPAINT_MIN_SEC,
        max: MUSIC_REPAINT_MAX_SEC,
        cur: Math.round(span),
      });
    }
    return '';
  })();

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
        {/* 该玩法的提示词写作建议，悬停向右展开（没配也没内置默认时整体不渲染） */}
        <div className='ml-auto flex-shrink-0 whitespace-nowrap'>
          <PromptGuideTip category='music' tabKey={mode} />
        </div>
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
            renderOptionItem={makeModelOptionRenderer(noteOf)}
            emptyContent={t('当前分组下暂无音乐模型')}
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

        {/* 辅助语言模型:两个用途共用一套选择 —— 音效的中译英,和文生音乐的「AI 帮我写词」。
            都是单次非流式打 /pg/chat/completions,没必要让用户选两次。
            先选分组再选模型,其余参数(temperature 等)后端默认,不暴露。 */}
        {(showTranslation || showAssistModel) && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Languages size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {showTranslation ? t('语言模型') : t('辅助语言模型')}
              </Typography.Text>
              <Tooltip
                content={
                  showTranslation
                    ? t('中文将自动翻译为英文后生成')
                    : t('用于「AI 帮我写词」:据你的描述拟出歌词与曲式参数')
                }
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Select
              placeholder={t('请选择分组')}
              selection
              filter={selectFilter}
              autoClearSearchValue={false}
              onChange={(value) => onInputChange('translationGroup', value)}
              value={inputs.translationGroup}
              optionList={translationGroupOptions}
              renderOptionItem={renderGroupOption}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg mb-2'
            />
            <Select
              placeholder={t('请选择模型')}
              selection
              filter={selectFilter}
              autoClearSearchValue={false}
              onChange={(value) => onInputChange('translationModel', value)}
              value={inputs.translationModel}
              optionList={translationModelOptions}
              emptyContent={t('当前分组下暂无可用语言模型')}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

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
            {/* 从「改编风格 / 重绘片段」跳进来时源音频已定(task:<task_id>),不必也不该
                再让用户上传;想换素材点「改为上传文件」清掉引用即可。 */}
            {inputs.srcTaskId ? (
              <div className='flex items-center gap-2'>
                <Typography.Text className='text-xs text-gray-600'>
                  {inputs.srcTaskLabel || t('上一首生成结果')}
                </Typography.Text>
                {!disabled && (
                  <Button
                    theme='borderless'
                    type='tertiary'
                    size='small'
                    onClick={() => {
                      onInputChange('srcTaskId', '');
                      onInputChange('srcTaskLabel', '');
                    }}
                  >
                    {t('改为上传文件')}
                  </Button>
                )}
              </div>
            ) : (
              !disabled && (
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
              )
            )}
            {!inputs.srcTaskId && inputs.audioData && (
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
              {/* 「AI 帮我写词」按钮在对话区输入框旁边 —— 它要拿输入框里的描述当输入,
                  而那个值在 MusicChatArea 的本地 state 里。 */}
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

        {/* 时长(仅文生音乐)。改编/重绘不显示:引擎里
              if params.task_type in ("cover", "repaint", ...): audio_duration = None
            (inference.py:819)——产出长度锁死为源音频长度,下发多少都被静默忽略。
            摆出来就是个假开关,同视频页 s2v 的处理。 */}
        {mode === 't2m' && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Clock size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('目标时长')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '模型把它当作参考锚点而非精确指令,成品会在附近浮动。30~60 秒与 2~4 分钟最稳定。需先填写歌词才会生效。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
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

        {/* 改编强度(仅 cover)。官方标为 cover 的 Key parameter:越高越贴原曲结构,
            越低越自由。原先没暴露,用户只能吃引擎默认的 1.0(最大保留),等于"改编"改不动。 */}
        {mode === 'cover' && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <SlidersHorizontal size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('保留原曲结构')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '1 = 最大限度保留原曲结构(引擎默认);越低越自由发挥,改动越大。留空走引擎默认。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <InputNumber
              min={0}
              max={1}
              step={0.1}
              value={
                inputs.coverStrength === '' ? undefined : inputs.coverStrength
              }
              onChange={(v) => onInputChange('coverStrength', v ?? '')}
              placeholder={t('留空 = 默认 {{v}}', {
                v: MUSIC_DEFAULT_COVER_STRENGTH,
              })}
              disabled={disabled}
              style={{ width: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 重绘区间与力度(仅 repaint)。不填区间时引擎默认 start=0/end=-1 → 全曲重绘,
            那就跟改编没区别了 —— repaint 的价值正在于只改一段。 */}
        {mode === 'repaint' && (
          <>
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <Clock size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('重绘区间(秒)')}
                </Typography.Text>
                <Tooltip
                  content={t(
                    '只重绘这一段,其余保持原样。引擎可操作范围 {{min}}~{{max}} 秒;两个都留空 = 全曲重绘。',
                    { min: MUSIC_REPAINT_MIN_SEC, max: MUSIC_REPAINT_MAX_SEC },
                  )}
                  position='top'
                >
                  <HelpCircle size={14} className='text-gray-400 cursor-help' />
                </Tooltip>
              </div>
              <div className='flex items-center gap-2'>
                <InputNumber
                  min={0}
                  value={
                    inputs.repaintStart === '' ? undefined : inputs.repaintStart
                  }
                  onChange={(v) => onInputChange('repaintStart', v ?? '')}
                  placeholder={t('起')}
                  disabled={disabled}
                  style={{ flex: 1 }}
                  className='!rounded-lg'
                />
                <span className='text-gray-400'>-</span>
                <InputNumber
                  min={0}
                  value={
                    inputs.repaintEnd === '' ? undefined : inputs.repaintEnd
                  }
                  onChange={(v) => onInputChange('repaintEnd', v ?? '')}
                  placeholder={t('止')}
                  disabled={disabled}
                  style={{ flex: 1 }}
                  className='!rounded-lg'
                />
              </div>
              {repaintRangeWarning && (
                <Typography.Text type='warning' className='text-xs mt-1 block'>
                  {repaintRangeWarning}
                </Typography.Text>
              )}
            </div>
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <SlidersHorizontal size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('重绘方式')}
                </Typography.Text>
              </div>
              <Select
                value={inputs.repaintMode}
                onChange={(v) => onInputChange('repaintMode', v)}
                optionList={MUSIC_REPAINT_MODES.map((o) => ({
                  label: t(o.label),
                  value: o.value,
                }))}
                disabled={disabled}
                style={{ width: '100%' }}
                dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                className='!rounded-lg'
              />
              {/* 强度只在 balanced 模式生效(引擎侧语义),其余模式不显示也不下发。 */}
              {inputs.repaintMode === 'balanced' && (
                <InputNumber
                  min={0}
                  max={1}
                  step={0.1}
                  value={
                    inputs.repaintStrength === ''
                      ? undefined
                      : inputs.repaintStrength
                  }
                  onChange={(v) => onInputChange('repaintStrength', v ?? '')}
                  placeholder={t('重绘强度,留空 = 默认 {{v}}', {
                    v: MUSIC_DEFAULT_REPAINT_STRENGTH,
                  })}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  className='!rounded-lg mt-2'
                />
              )}
            </div>
          </>
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
                  {/* 调式:与 BPM 同类的曲式元数据,官方建议不要写进描述而走独立字段。
                      格式必须是「音名[升降号] major|minor」且 mode 小写(引擎
                      constants.py VALID_KEYSCALES),写成 "Am" / "C Major" 都会被静默丢弃。 */}
                  <div>
                    <Typography.Text className='text-xs text-gray-600 block mb-1'>
                      {t('调式')}
                    </Typography.Text>
                    <Input
                      value={inputs.keyScale}
                      onChange={(v) => onInputChange('keyScale', v)}
                      placeholder={t('如 C major / A minor;留空 = 自动')}
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
