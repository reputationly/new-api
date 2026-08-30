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
  MUSIC_DEFAULT_COVER_STRENGTH,
  MUSIC_REPAINT_MODES,
  MUSIC_DEFAULT_REPAINT_STRENGTH,
  MUSIC_REPAINT_MIN_SEC,
  MUSIC_REPAINT_MAX_SEC,
  musicDefaultStepsForEngine,
  musicDefaultGuidanceForEngine,
  MUSIC_ENGINE_MINIMAX_MUSIC3,
  MUSIC3_DURATIONS,
} from '../../constants/musicPlayground.constants';

// 音乐模型配置面板:分组/模型(同视频/语音)+ 按 mode 的输入:
//   - acestep(cover/repaint):驱动音频上传(可试听)+ 歌词 + 时长 + BPM/演唱语言;
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
  audioLabel = '',
  refAudioMaxMB = MUSIC_AUDIO_UPLOAD_MAX_MB,
  videoMaxMB = MUSIC_VIDEO_UPLOAD_MAX_MB,
  styleState,
}) => {
  const { t } = useTranslation();
  const fileInputRef = useRef(null);

  // engine 是**模型声明的引擎族**(useMusicGeneration 返回 resolvedEngine),不是 tab
  // 默认值 —— 文生音乐这个 tab 同时挂着 ACE-Step 与 MiniMax-Music3,按 tab 判会把
  // Music3 也当成 ACE-Step 渲染。
  const isAceStep = engine === 'acestep';
  const isMusic3 = engine === MUSIC_ENGINE_MINIMAX_MUSIC3;

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
  // 时长下拉:'' → 「自动(引擎默认)」,其余为秒数。
  const durationOptions = MUSIC_DURATIONS.map((d) =>
    d === ''
      ? { label: t('自动(引擎默认)'), value: '' }
      : { label: t('{{sec}} 秒', { sec: d }), value: d },
  );

  // 占位默认:采样步数 8 / guidance 7(ACE-Step,与 deploy-config 一致,所见即所发)。
  // AudioX/SoulX 下线后只剩这一档,取值函数已退化成常量返回。
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

        {/* 歌词。ACE-Step:可选,留空由模型按描述自动生成。
            MiniMax-Music3:**必填**,且它就是引擎的 input(描述另走 instructions);
            门面对 task_type=tts 硬校验 prompt 非空,空着提交会被本地拦下。 */}
        {(isAceStep || isMusic3) && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <FileText size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {isMusic3 ? t('歌词（必填）') : t('歌词')}
              </Typography.Text>
              <Tooltip
                content={
                  isMusic3
                    ? t(
                        '必填。这一栏就是模型要唱的内容,下方描述只决定编曲。可用 [Verse]/[Chorus]/[Bridge]/[Instrumental] 等段落标签,按官方要求每个标签单独占一行。留空无法提交。',
                      )
                    : t(
                        '可选。留空则由模型按描述自动生成歌词;填写则按此歌词演唱。支持 [verse]/[chorus]/[bridge] 等结构标签分段。',
                      )
                }
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
              {/* 「AI 优化提示词」按钮在对话区输入框旁边 —— 它要拿输入框里的描述当输入,
                  而那个值在 MusicChatArea 的本地 state 里。Music3 上没有那个按钮
                  (它回填的 BPM/调式/时长 Music3 都没有),换成「AI 优化提示词」。 */}
            </div>
            <TextArea
              placeholder={
                isMusic3
                  ? t(
                      '必填,输入要演唱的歌词。段落标签单独占一行,如\n[Verse]\n...\n[Chorus]\n...',
                    )
                  : t(
                      '可选,输入歌词;留空则自动生成。可用 [verse] / [chorus] / [bridge] 分段',
                    )
              }
              value={inputs.lyrics}
              onChange={(value) => onInputChange('lyrics', value)}
              autosize={{ minRows: 5, maxRows: 14 }}
              disabled={disabled}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 时长(仅 ACE-Step 的文生音乐)。改编/重绘不显示:引擎里
              if params.task_type in ("cover", "repaint", ...): audio_duration = None
            (inference.py:819)——产出长度锁死为源音频长度,下发多少都被静默忽略。
            摆出来就是个假开关,同视频页 s2v 的处理。
            Music3 不走这个控件,但**有它自己的时长控件**(见下),语义不同:
            ACE-Step 的 audio_duration 是参考锚点,Music3 的 max_new_tokens 是帧数上限。 */}
        {mode === 't2m' && isAceStep && (
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

        {/* 目标时长(仅 MiniMax-Music3)。下发 max_new_tokens = 秒 × 25(引擎 25 fps),
            官方 curl 即此形态("max_new_tokens": 750 = 30 秒)。
            **它是上限不是目标**:模型吐出 end-of-audio 就提前结束,所以文案说"最长",
            不能照搬 ACE-Step 那句"锚点、成品在附近浮动"—— 两者语义相反,说错会让用户
            以为选 60 就一定出 60 秒。 */}
        {isMusic3 && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Clock size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('最长时长')}
              </Typography.Text>
              <Tooltip
                content={t(
                  '生成长度的**上限**,不是目标值:歌唱完模型会自己收尾,成品通常短于这个数。留空则由引擎默认决定。最长可选 5 分钟——那是模型卡声明的完整歌曲长度;引擎侧还有一道 9000 帧(6 分钟)的硬上限,只做钳位用。',
                )}
                position='top'
              >
                <HelpCircle size={14} className='text-gray-400 cursor-help' />
              </Tooltip>
            </div>
            <Select
              name='music3Duration'
              selection
              onChange={(value) => onInputChange('music3Duration', value)}
              value={inputs.music3Duration}
              optionList={MUSIC3_DURATIONS.map((d) =>
                d === ''
                  ? { label: t('自动(引擎默认)'), value: '' }
                  : { label: t('最长 {{sec}} 秒', { sec: d }), value: d },
              )}
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

        {/* 高级参数(默认折叠,全部选填;留空即走引擎默认)。
            Music3 也展示这一块,但里面只留它真支持的项(seed)——guidance / steps /
            演唱语言 / BPM / 调式是 ACE-Step 专属,对它是假开关。
            ⚠️ 曾经这里对 Music3 整块隐藏,理由是"它的下发分支里没有这些字段"——那是
            用"我们没实现"论证"模型不支持",反了:官方 OpenAICreateSpeechRequest 里
            seed 与 max_new_tokens 都是一等字段,官方 curl 就带着它们。 */}
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

              {/* Guidance / 采样步数:ACE-Step 专属。Music3 的这两个量固定在部署
                  config 里(cfg_scale/top_k 见引擎 constants.py),请求侧没有对应字段,
                  摆出来是假开关。 */}
              {!isMusic3 && (
                <>
                  <div>
                    <div className='flex items-center gap-2 mb-1'>
                      <Typography.Text className='text-xs text-gray-600'>
                        {t('贴合度 (guidance)')}
                      </Typography.Text>
                      <Tooltip
                        content={t(
                          '越高越贴合描述,越低越自由;留空 = 引擎默认。',
                        )}
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

                  <div>
                    <div className='flex items-center gap-2 mb-1'>
                      <Typography.Text className='text-xs text-gray-600'>
                        {t('采样步数 (steps)')}
                      </Typography.Text>
                      <Tooltip
                        content={t('越大越精细但越慢;留空 = 引擎默认。')}
                      >
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
                </>
              )}
            </div>
          </Collapse.Panel>
        </Collapse>
      </div>
    </Card>
  );
};

export default MusicConfigPanel;
