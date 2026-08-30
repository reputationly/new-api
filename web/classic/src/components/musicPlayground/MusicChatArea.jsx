import React, { useState, useMemo, useCallback, useContext } from 'react';
import {
  Card,
  Chat,
  Button,
  Typography,
  Progress,
  Spin,
  TextArea,
} from '@douyinfe/semi-ui';
import { Download, RefreshCw, Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { showError, getLogo, stringToColor } from '../../helpers';
import { UserContext } from '../../context/User';
import { blockChatDrag } from '../playground/blockChatDrag';
import AiAssistButton from '../playground/AiAssistButton';
import PromptOptimizeButton from '../playground/PromptOptimizeButton';
import {
  MUSIC_STATUS,
  musicExamplesForMode,
  MUSIC_ENGINE_MINIMAX_MUSIC3,
} from '../../constants/musicPlayground.constants';

// 音乐模型对话区:成品渲染 <audio> 播放器 + 下载。格式无关(ACE-Step .mp3 /
// MiniMax-Music3 .wav)——下载文件名从返回的 content-url + 响应 media type 推断,
// 不硬编码扩展名。预设/占位/欢迎语随引擎族(ACE-Step / Music3)变化。

const WELCOME_ID = '__welcome__';
const MAX_PROMPT_LEN = 5000;

const genUserAvatar = (username) => {
  if (!username) return getLogo();
  const firstLetter = username[0].toUpperCase();
  const bgColor = stringToColor(username);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="${bgColor}"/><text x="50%" y="50%" dominant-baseline="central" text-anchor="middle" font-size="16" fill="#ffffff" font-family="sans-serif">${firstLetter}</text></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
};

const parseTs = (id, fallback) => {
  const n = Number(String(id).split('-')[1]);
  return Number.isFinite(n) ? n : fallback;
};

// 从 URL / 响应 media type 推断音频扩展名(格式无关:.mp3 / .wav / .flac 等)。
const extFromContentType = (ct) => {
  if (!ct) return '';
  const type = ct.split(';')[0].trim().toLowerCase();
  const map = {
    'audio/mpeg': 'mp3',
    'audio/mp3': 'mp3',
    'audio/wav': 'wav',
    'audio/x-wav': 'wav',
    'audio/wave': 'wav',
    'audio/flac': 'flac',
    'audio/ogg': 'ogg',
    'audio/webm': 'webm',
    'audio/mp4': 'm4a',
    'audio/aac': 'aac',
  };
  return map[type] || '';
};

const extFromUrl = (url) => {
  try {
    const path = new URL(url, window.location.origin).pathname;
    const m = path.match(/\.([a-z0-9]{2,4})$/i);
    return m ? m[1].toLowerCase() : '';
  } catch (e) {
    return '';
  }
};

// 下载生成的音频:优先响应 content-type,其次 URL 后缀,兜底 mp3。
const downloadAudio = async (url, t) => {
  try {
    const resp = await fetch(url, { credentials: 'include' });
    const blob = await resp.blob();
    const ext =
      extFromContentType(resp.headers.get('content-type')) ||
      extFromContentType(blob.type) ||
      extFromUrl(url) ||
      'mp3';
    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = `music-${Date.now()}.${ext}`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  } catch (e) {
    showError(t('下载失败'));
  }
};

// 生成中:三阶段 + 进度(任务通常无百分比,走 Spin 文案)。
const MusicProgress = ({ status, progress, t }) => {
  const current = status === MUSIC_STATUS.QUEUED ? 0 : 1;
  const stages = [t('排队中'), t('生成中'), t('完成')];
  const hasPercent = typeof progress === 'number' && progress > 0;
  return (
    <div
      className='flex flex-col items-center gap-4 py-4 px-2 mx-auto'
      style={{ minWidth: 300, maxWidth: 420 }}
    >
      <div className='flex items-center justify-center gap-3 flex-wrap'>
        {stages.map((label, i) => (
          <React.Fragment key={i}>
            <div className='flex items-center gap-1.5 whitespace-nowrap'>
              <span
                className='flex items-center justify-center rounded-full text-xs'
                style={{
                  width: 18,
                  height: 18,
                  background:
                    i <= current ? 'var(--semi-color-primary)' : '#e5e7eb',
                  color: i <= current ? '#fff' : '#6b7280',
                }}
              >
                {i + 1}
              </span>
              <span
                className='text-sm'
                style={{
                  color:
                    i === current
                      ? 'var(--semi-color-primary)'
                      : i < current
                        ? '#4b5563'
                        : '#9ca3af',
                  fontWeight: i === current ? 600 : 400,
                }}
              >
                {label}
              </span>
            </div>
            {i < stages.length - 1 && (
              <span style={{ color: '#d1d5db' }}>—</span>
            )}
          </React.Fragment>
        ))}
      </div>
      <div className='flex items-center justify-center gap-2 w-full'>
        {hasPercent ? (
          <>
            <Progress
              percent={progress}
              stroke='var(--semi-color-primary)'
              style={{ flex: 1, maxWidth: 260 }}
            />
            <Typography.Text className='text-xs text-gray-500'>
              {progress}%
            </Typography.Text>
          </>
        ) : (
          <div className='flex items-center gap-2 text-gray-500 text-sm'>
            <Spin size='small' />
            {status === MUSIC_STATUS.QUEUED ? t('任务排队中…') : t('生成中…')}
          </div>
        )}
      </div>
    </div>
  );
};

const MusicChatArea = ({
  messages,
  generating,
  turnLimitReached = false,
  missingRequiredAudio = false,
  engine = 'acestep',
  mode = 't2m',
  needsText = true,
  showTranslation = false,
  englishOnlyNoTranslate = false,
  welcomeText = '',
  onApplyExample,
  onSend,
  drafting = false,
  onDraftPlan,
  onSendToSrc,
  onRegenerate,
  onRefetch,
  onClear,
}) => {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const [inputValue, setInputValue] = useState('');
  // AI 优化提示词在途:此刻允许发送等于把没优化的原文发出去,故一并灰掉发送按钮。
  const [optimizing, setOptimizing] = useState(false);

  const isAceStep = engine === 'acestep';
  // Music3 的输入框语义与 ACE-Step 相反:这里写的是**编曲说明**(→ instructions),
  // 歌词在左侧面板(→ 引擎 input)。文案必须说清,否则用户会把歌词写进这里,
  // 而那样只会得到一段"按歌词描述编出来的伴奏",不报错。
  const isMusic3 = engine === MUSIC_ENGINE_MINIMAX_MUSIC3;
  // 一键示例(按 mode + 引擎族):cover/repaint 带驱动音;t2m 两套引擎各一份。
  const presets = musicExamplesForMode(mode, engine);
  const showPresets = presets.length > 0;

  const defaultWelcome = isMusic3
    ? t('欢迎使用 AI 文生音乐,请在左侧填写歌词,并在下方描述曲风与编配')
    : isAceStep
      ? t('欢迎使用 AI 文生音乐,请在左侧选择模型,并在下方输入音乐风格描述')
      : t('欢迎使用 AI 音频生成,请在左侧选择模型并配置输入');

  // 只剩 ACE-Step 与 Music3 两族;最后一档是兜底,防运营声明了未知引擎时输入框没占位。
  const placeholder = isMusic3
    ? t('描述曲风、乐器编配、速度与情绪(歌词写在左侧)')
    : isAceStep
      ? t('请输入音乐风格描述')
      : t('请输入描述');

  const roleConfig = useMemo(
    () => ({
      user: {
        name: userState?.user?.username || 'User',
        avatar: genUserAvatar(userState?.user?.username),
      },
      assistant: { name: t('音乐模型'), avatar: getLogo() },
      system: { name: 'System', avatar: getLogo() },
    }),
    [userState, t],
  );

  const chats = useMemo(() => {
    if (!messages.length) {
      return [
        {
          role: 'assistant',
          id: WELCOME_ID,
          createAt: 1,
          content: welcomeText || defaultWelcome,
        },
      ];
    }
    return messages.map((m, i) => {
      if (m.role === 'user') {
        return {
          role: 'user',
          id: m.id,
          createAt: parseTs(m.id, i + 1),
          content: m.content,
        };
      }
      const done =
        m.status === MUSIC_STATUS.COMPLETED ||
        m.status === MUSIC_STATUS.FAILED ||
        m.status === MUSIC_STATUS.CANCELED;
      return {
        role: 'assistant',
        id: m.id,
        createAt: parseTs(m.id, i + 1),
        status: done
          ? m.status === MUSIC_STATUS.FAILED
            ? 'error'
            : 'complete'
          : 'loading',
        content:
          m.status === MUSIC_STATUS.FAILED ? m.error || t('生成失败') : '',
      };
    });
  }, [messages, welcomeText, defaultWelcome, t]);

  const byId = useMemo(
    () => new Map(messages.map((m) => [m.id, m])),
    [messages],
  );

  const renderChatBoxContent = useCallback(
    ({ message, defaultContent }) => {
      const m = byId.get(message.id);
      if (!m) return defaultContent;
      // 用户消息:原文正下方用灰色小字挂译文对照(中译英)。无译文则原样渲染。
      if (m.role === 'user') {
        if (!m.translatedText) return defaultContent;
        return (
          <div>
            {defaultContent}
            <Typography.Text type='tertiary' className='text-xs block mt-1'>
              {`🌐 ${m.translatedText}`}
            </Typography.Text>
          </div>
        );
      }
      // 助手消息:翻译阶段(拿到 taskId 前)优先显示「翻译中…」,取代生成进度。
      if (m.translating) {
        return (
          <div className='flex items-center gap-2 text-gray-500 text-sm py-2'>
            <Spin size='small' />
            {t('翻译中…')}
          </div>
        );
      }
      if (m.status === MUSIC_STATUS.COMPLETED && m.musicUrl) {
        return (
          <div className='inline-block' style={{ minWidth: 320 }}>
            <Typography.Text className='text-sm text-gray-600 block mb-2'>
              {t('已生成')}
            </Typography.Text>
            <audio
              src={m.musicUrl}
              controls
              preload='metadata'
              style={{ width: 320 }}
            />
            <div className='flex items-center gap-1 mt-2'>
              <Button
                theme='borderless'
                type='tertiary'
                size='small'
                icon={<Download size={14} />}
                onClick={() => downloadAudio(m.musicUrl, t)}
                className='!text-gray-500'
              />
              <Button
                theme='borderless'
                type='tertiary'
                size='small'
                icon={<RefreshCw size={14} />}
                onClick={() => onRegenerate(m.prompt)}
                disabled={generating}
                className='!text-gray-500'
              />
              {/* 拿这首继续加工:切到改编/重绘并把它作为源音频。发的是 task:<task_id>,
                  后端在共享盘上直读产物(nfsinput/taskref.go),不必下载再上传一遍。 */}
              {onSendToSrc && m.taskId && (
                <>
                  <Button
                    theme='borderless'
                    type='tertiary'
                    size='small'
                    onClick={() => onSendToSrc('cover', m.taskId)}
                    className='!text-gray-500 !text-xs'
                  >
                    {t('改编风格')}
                  </Button>
                  <Button
                    theme='borderless'
                    type='tertiary'
                    size='small'
                    onClick={() => onSendToSrc('repaint', m.taskId)}
                    className='!text-gray-500 !text-xs'
                  >
                    {t('重绘片段')}
                  </Button>
                </>
              )}
            </div>
          </div>
        );
      }
      if (m.status === MUSIC_STATUS.FAILED) {
        return (
          <div className='inline-block'>
            <Typography.Text type='danger' className='text-sm block mb-1'>
              {m.error || t('生成失败')}
            </Typography.Text>
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={<RefreshCw size={14} />}
              onClick={() => onRegenerate(m.prompt)}
              disabled={generating}
              className='!text-gray-500'
            />
          </div>
        );
      }
      if (m.status === MUSIC_STATUS.CANCELED) {
        return (
          <div className='inline-block'>
            <Typography.Text type='tertiary' className='text-sm block mb-1'>
              {t('已取消')}
            </Typography.Text>
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={<RefreshCw size={14} />}
              onClick={() => onRegenerate(m.prompt)}
              disabled={generating}
              className='!text-gray-500'
            />
          </div>
        );
      }
      if (m.pollTimedOut) {
        return (
          <div className='inline-block'>
            <Typography.Text type='tertiary' className='text-sm block mb-1'>
              {t('生成时间较长，任务仍在后台处理')}
            </Typography.Text>
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={<RefreshCw size={14} />}
              onClick={() => onRefetch(m.id, m.taskId)}
              className='!text-gray-500'
            >
              {t('继续获取')}
            </Button>
          </div>
        );
      }
      return <MusicProgress status={m.status} progress={m.progress} t={t} />;
    },
    [byId, generating, onRegenerate, onRefetch, t],
  );

  const renderInputArea = useCallback(() => {
    // 缺必填上传/生成中/达上限时置灰。
    const blockSend = generating || turnLimitReached || missingRequiredAudio;
    const hasText = inputValue.trim().length > 0;
    // 优化 / 拟稿在途时同样不能发:此刻发出去的还是没经 AI 处理的原文,而拟稿更狠 ——
    // 歌词还没落到左侧,提交就会命中 sample_mode,选好的时长/BPM 又被引擎覆盖掉。
    const canSend =
      !blockSend && !optimizing && !drafting && (needsText ? hasText : true);
    const doSend = () => {
      if (!canSend) return;
      onSend(inputValue.trim());
      setInputValue('');
    };
    return (
      <div className='p-2 sm:p-4'>
        {/* Music3 换一套说法。它不是「仅支持英文」——官方只是所有 caption 示例都用英文,
            没说中文不行;而且照搬原句会让人以为歌词也被译了,那正好是要避免的误解:
            歌词是唱出来的内容,任何情况下都保持原文(同 H3 对台词的处理)。 */}
        {showTranslation && (
          <Typography.Text
            type='tertiary'
            className='text-xs block mb-2 text-center'
          >
            {isMusic3
              ? t('已开启自动翻译:曲风描述会译成英文，歌词保持原文')
              : t('当前模型仅支持英文,已开启语言模型自动翻译')}
          </Typography.Text>
        )}
        {/* 模型只认英文、但运营没配翻译模型:这句得在写之前说。写完中文再发,只会
            吃一句「请直接用英文描述」的报错,白写一遍。 */}
        {englishOnlyNoTranslate && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {isMusic3
              ? t(
                  '未配置翻译模型:曲风描述建议直接写英文（歌词不受影响，照常写中文）',
                )
              : t('当前模型仅支持英文,请直接用英文描述')}
          </Typography.Text>
        )}
        {turnLimitReached && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {t('本轮对话已达生成上限，请点击右侧「新对话」继续')}
          </Typography.Text>
        )}
        {missingRequiredAudio && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {t('请先在左侧上传驱动音频')}
          </Typography.Text>
        )}
        {/* 一键示例:纯文本(仅填输入框)或结构化对象({label,prompt,params,files}——
            同时预置驱动音/双音频等文件)。单行等宽排列,超长 CSS 截断。 */}
        {showPresets && (
          <div className='flex gap-2 mb-2 overflow-hidden'>
            {presets.map((ex, i) => {
              const isObj = ex && typeof ex === 'object';
              const promptText = isObj ? ex.prompt : ex;
              const label = isObj ? ex.label : ex;
              return (
                <button
                  key={i}
                  type='button'
                  title={promptText || label}
                  onClick={() => {
                    setInputValue(promptText || '');
                    if (isObj) onApplyExample?.(ex);
                  }}
                  className='flex-1 min-w-0 truncate text-xs text-gray-600 bg-gray-100 hover:bg-gray-200 rounded-full px-3 py-1.5 transition-colors'
                >
                  {label}
                </button>
              );
            })}
          </div>
        )}
        {/* 「AI 帮我写词」= 官方 Simple Mode 的【Create Sample】那一步:据这句描述拟出
            caption/歌词/BPM/调式/时长,caption 回填到输入框、其余回填到左侧面板,由用户
            过目再改。这一步不只是省事 —— 填了歌词之后提交就不再走 sample_mode,引擎那边
            「用 LM 自己推的时长覆盖用户值」的逻辑不触发,时长/BPM 才真正生效。
            外观与交互走 AiAssistButton,与各体验区的「AI 优化提示词」同一份:空输入不置灰
            (点了给提示),在途转圈换文案。 */}
        {onDraftPlan && (
          <AiAssistButton
            label={t('AI 帮我写词')}
            busyLabel={t('拟稿中…')}
            hint={t('先拟好歌词与曲式再生成,左侧的时长/速度才会生效')}
            busyHint={t('正在拟稿，请勿刷新或切换页面，否则要重新来一次')}
            busy={drafting}
            disabled={generating}
            onClick={async () => {
              const caption = await onDraftPlan(inputValue.trim());
              if (typeof caption === 'string' && caption) {
                setInputValue(caption);
              }
            }}
          />
        )}
        {/* 「AI 优化提示词」出现在 MiniMax-Music3 的文生音乐上:它的输入是一句
            "要什么编曲"的描述,补全成官方 Structured Caption 能直接提升产出质量。

            **`!onDraftPlan` 这个条件不能省**。promptOptimize 是 **tab 级**声明,
            usePromptOptimize 的 available 只看 tab 不看引擎 —— 光靠 draftAvailable
            排除 Music3 只做了一半:ACE-Step 那边「AI 帮我写词」照旧渲染,而优化按钮
            也跟着渲染出来,两个并排。而且 ACE-Step 的 t2m 没有专用优化模板,点了走的是
            通用兜底,对它的 caption 帮不上忙。
            两个按钮是同一件事的两种做法,用同一个判据取反即可:写词按钮在
            (onDraftPlan 由页面按 draftAvailable 传)就不出优化按钮,反之才出。

            engine 必须传:文生音乐这个 tab 同时挂 ACE-Step 与 Music3,而两者的描述位
            语义相反(caption vs 编曲说明 instructions),不传就会拿通用模板去优化,
            产出一段 Music3 用不上的文案 —— 同视频页 H3 那次的教训。 */}
        {!onDraftPlan && (
          <PromptOptimizeButton
            category='music'
            tabKey={mode}
            engine={engine}
            value={inputValue}
            onChange={setInputValue}
            disabled={generating}
            onOptimizingChange={setOptimizing}
          />
        )}
        <div className='relative'>
          <TextArea
            value={inputValue}
            onChange={setInputValue}
            placeholder={placeholder}
            maxLength={MAX_PROMPT_LEN}
            autosize={{ minRows: 2, maxRows: 6 }}
            className='!rounded-xl'
            style={{ paddingRight: 46 }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                doSend();
              }
            }}
          />
          <Button
            theme='solid'
            onClick={doSend}
            disabled={!canSend}
            icon={<Send size={16} className='text-white' />}
            className='!rounded-full !bg-blue-500 hover:!bg-blue-600'
            style={{
              position: 'absolute',
              right: 8,
              bottom: 8,
              width: 32,
              height: 32,
              minWidth: 32,
              padding: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          />
        </div>
      </div>
    );
  }, [
    generating,
    turnLimitReached,
    missingRequiredAudio,
    showTranslation,
    englishOnlyNoTranslate,
    // engine / isMusic3:翻译提示文案与「AI 优化提示词」的模板都按引擎族分叉,
    // 漏进依赖会让切模型后这块仍按上一个引擎渲染(useCallback 的记忆值不刷新)。
    engine,
    isMusic3,
    needsText,
    showPresets,
    presets,
    placeholder,
    onApplyExample,
    inputValue,
    optimizing,
    onSend,
    drafting,
    onDraftPlan,
    mode,
    t,
  ]);

  return (
    <Card
      className='h-full pg-chat-scroll'
      bordered={false}
      bodyStyle={{
        padding: 0,
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      }}
    >
      <div
        style={{ height: '100%' }}
        onDragOverCapture={blockChatDrag}
        onDropCapture={blockChatDrag}
      >
        <Chat
          chats={chats}
          roleConfig={roleConfig}
          onMessageSend={(content) => onSend(content)}
          onClear={onClear}
          renderInputArea={renderInputArea}
          chatBoxRenderConfig={{
            renderChatBoxContent,
            renderChatBoxTitle: () => null,
            renderChatBoxAction: () => null,
          }}
          showClearContext
          placeholder={placeholder}
          style={{ height: '100%' }}
        />
      </div>
    </Card>
  );
};

export default MusicChatArea;
