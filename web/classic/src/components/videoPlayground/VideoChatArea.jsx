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
import PromptOptimizeButton from '../playground/PromptOptimizeButton';
import OptimizedPromptSections from './OptimizedPromptSections';
import H3PromptFields, { H3MainFieldLabel } from './H3PromptFields';
import {
  VIDEO_STATUS,
  videoExamplesForMode,
  VIDEO_ENGINE_MINIMAX_H3,
} from '../../constants/videoPlayground.constants';
import {
  parseH3Prompt,
  joinH3Prompt,
  buildLocalH3Prompt,
  h3HasField,
  h3MainKey,
} from '../../constants/h3Prompt.constants';

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

const downloadVideo = async (url, t) => {
  try {
    const resp = await fetch(url, { credentials: 'include' });
    const blob = await resp.blob();
    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = `video-${Date.now()}.mp4`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  } catch (e) {
    showError(t('下载失败'));
  }
};

// 生成中：精简阶段（文字不缩略）+ 进度。流水线按 pipeline 动态插入
// 「画质增强」（超分）/「配音」阶段：生成 →[画质增强]→[配音]→ 完成。
const VideoProgress = ({ status, progress, stage, pipeline, t }) => {
  const hasUpscale = !!pipeline?.upscale;
  const hasDub = !!pipeline?.dub;
  const stages = [t('排队中'), t('生成中')];
  const stageKeys = ['queued', 'generating'];
  if (hasUpscale) {
    stages.push(t('画质增强'));
    stageKeys.push('upscaling');
  }
  if (hasDub) {
    stages.push(t('配音'));
    stageKeys.push('dubbing');
  }
  stages.push(t('完成'));
  stageKeys.push('done');
  const curKey =
    stage === 'upscaling'
      ? 'upscaling'
      : stage === 'dubbing'
        ? 'dubbing'
        : status === VIDEO_STATUS.QUEUED
          ? 'queued'
          : 'generating';
  const current = stageKeys.indexOf(curKey);
  const hasPercent = typeof progress === 'number' && progress > 0;
  return (
    <div
      className='flex flex-col items-center gap-4 py-4 px-2 mx-auto'
      style={{ minWidth: 300, maxWidth: 420 }}
    >
      {/* 阶段指示：whitespace-nowrap 保证文字完整不被缩略 */}
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
            {stage === 'upscaling'
              ? t('画质增强中…')
              : stage === 'dubbing'
                ? t('配音中…')
                : status === VIDEO_STATUS.QUEUED
                  ? t('任务排队中…')
                  : t('生成中…')}
          </div>
        )}
      </div>
    </div>
  );
};

const VideoChatArea = ({
  messages,
  generating,
  turnLimitReached = false,
  missingRequiredImage = false,
  mode = 'text2video',
  // 提示词优化配置按「分类 + tab」读:视频配音(dub)的入口在语音页,配置也存在 audio 下。
  category = 'video',
  selectedModel = '',
  optimizeEngine = '',
  optimizeContext = '',
  h3AlignContext = null,
  isSR = false,
  isDub = false,
  keyframeMode = 'i2v',
  onApplyExample,
  onSend,
  onRegenerate,
  onRefetch,
  onClear,
}) => {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  // 受控输入框:预设按钮直接 setInputValue,发送后清空(缺图/上限时不清空,提示词不丢)。
  const [inputValue, setInputValue] = useState('');
  // AI 优化提示词在途:此刻允许发送等于把没优化的原文发出去,故一并灰掉发送按钮。
  const [optimizing, setOptimizing] = useState(false);
  // 结构化的优化结果(MiniMax H3 的分段提示词)。非空时提交的是它回拼出来的文本,
  // 上方输入框退回「你的想法」的角色,只用来重新优化。切不出结构就一直是 null,
  // 优化结果照旧回填输入框——降级路径必须存在:模型偶尔不按格式返回是常态。
  const [sections, setSections] = useState(null);
  // MiniMax H3 的另外两段中文输入(音景 / 背景音乐)。画面描述沿用下面那个输入框。
  // 判据用运营在「视频模型配置」里声明的引擎族,不按模型名前缀猜 —— 后端的请求整形
  // (帧数约定/时长字段/画布推导)也是认这个字段,两边必须同源,否则自建部署把模型
  // 改个名就会「后端按 H3 发、前端给通用界面」。
  const isH3 = optimizeEngine === VIDEO_ENGINE_MINIMAX_H3 && !isSR && !isDub;
  const [h3Fields, setH3Fields] = useState({});
  // 一键示例(按 mode):text2video 纯文本;i2v/flf2v/s2v/vace/sr 带预置文件。
  // 关键帧还要按所选模型过滤:i2v 模型只出仅首帧的示例,flf2v 模型只出带尾帧的。
  // H3 另换一整张表(视听一体,示例要连音景与配乐一起给),故把引擎族也传进去。
  // 传原始 optimizeEngine 而不是 isH3:超分与配音在 H3 表里没有对应键,函数内部本来
  // 就会回退到通用表,不必在这里先滤一道。
  const presets = videoExamplesForMode(mode, keyframeMode, optimizeEngine);
  const hasPresets = presets.length > 0;

  const roleConfig = useMemo(
    () => ({
      user: {
        name: userState?.user?.username || 'User',
        avatar: genUserAvatar(userState?.user?.username),
      },
      assistant: { name: t('视频模型'), avatar: getLogo() },
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
          content: isSR
            ? t('欢迎使用 AI 视频超分，请在左侧上传源视频后点击下方按钮')
            : isDub
              ? t(
                  '欢迎使用 AI 视频配音，请在左侧上传待配音视频，并在下方描述画面里什么在发声，例如「脚步踩过落叶，远处有鸟叫」。本模型只做音效与环境音，不生成音乐和台词；画面将逐帧保持不变',
                )
              : t('欢迎使用 AI 视频生成，请在下方输入您的提示词'),
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
        m.status === VIDEO_STATUS.COMPLETED ||
        m.status === VIDEO_STATUS.FAILED ||
        m.status === VIDEO_STATUS.CANCELED;
      return {
        role: 'assistant',
        id: m.id,
        createAt: parseTs(m.id, i + 1),
        status: done
          ? m.status === VIDEO_STATUS.FAILED
            ? 'error'
            : 'complete'
          : 'loading',
        content:
          m.status === VIDEO_STATUS.FAILED ? m.error || t('视频生成失败') : '',
      };
    });
  }, [messages, isSR, isDub, t]);

  const byId = useMemo(
    () => new Map(messages.map((m) => [m.id, m])),
    [messages],
  );

  const renderChatBoxContent = useCallback(
    ({ message, defaultContent }) => {
      const m = byId.get(message.id);
      if (!m || m.role === 'user') return defaultContent;
      if (m.status === VIDEO_STATUS.COMPLETED && m.videoUrl) {
        return (
          <div className='inline-block'>
            <Typography.Text className='text-sm text-gray-600 block mb-2'>
              {t('视频已生成')}
            </Typography.Text>
            <video
              src={m.videoUrl}
              controls
              className='rounded-lg'
              style={{ maxWidth: 480, maxHeight: 360, background: '#000' }}
            />
            <div className='flex items-center gap-1 mt-2'>
              <Button
                theme='borderless'
                type='tertiary'
                size='small'
                icon={<Download size={14} />}
                onClick={() => downloadVideo(m.videoUrl, t)}
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
            </div>
          </div>
        );
      }
      if (m.status === VIDEO_STATUS.FAILED) {
        return (
          <div className='inline-block'>
            <Typography.Text type='danger' className='text-sm block mb-1'>
              {m.error || t('视频生成失败')}
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
      if (m.status === VIDEO_STATUS.CANCELED) {
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
      // 客户端轮询超时但任务仍可恢复：提示 + 「继续获取」（用原 taskId 续查，不重新提交）
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
      // queued / in_progress
      return (
        <VideoProgress
          status={m.status}
          progress={m.progress}
          stage={m.stage}
          pipeline={m.pipeline}
          t={t}
        />
      );
    },
    [byId, generating, onRegenerate, onRefetch, t],
  );

  const renderInputArea = useCallback(() => {
    // 缺必填帧图/生成中/达上限时置灰,回车与点击均不发送,提示词不丢。
    const blockSend = generating || turnLimitReached || missingRequiredImage;
    // 视频超分不需要提示词:只留一个生成按钮,上传源视频后可点,删除后再次置灰。
    // 常规比例的矩形按钮,对话框内水平居中、略上提,不做通栏扁条。
    if (isSR) {
      return (
        <div className='px-2 pb-6 sm:px-4 sm:pb-8 pt-1 flex flex-col items-center'>
          {turnLimitReached && (
            <Typography.Text
              type='warning'
              className='text-xs block mb-2 text-center'
            >
              {t('本轮对话已达生成上限，请点击右侧「新对话」继续')}
            </Typography.Text>
          )}
          {/* 一键示例(超分无提示词):点击预置源视频到左侧,再点生成。 */}
          {hasPresets && (
            <div className='flex gap-2 mb-3 overflow-hidden w-full max-w-sm'>
              {presets.map((ex, i) => {
                const isObj = ex && typeof ex === 'object';
                return (
                  <button
                    key={i}
                    type='button'
                    title={isObj ? ex.label : ex}
                    onClick={() => {
                      if (isObj) onApplyExample?.(ex);
                    }}
                    className='flex-1 min-w-0 truncate text-xs text-gray-600 bg-gray-100 hover:bg-gray-200 rounded-full px-3 py-1.5 transition-colors'
                  >
                    {isObj ? ex.label : ex}
                  </button>
                );
              })}
            </div>
          )}
          <Button
            theme='solid'
            size='large'
            onClick={() => onSend('')}
            disabled={blockSend}
            icon={<Send size={16} className={blockSend ? '' : 'text-white'} />}
            className={`!rounded-lg !px-8 !h-11 ${blockSend ? '' : '!bg-purple-500 hover:!bg-purple-600'}`}
          >
            {t('生成视频')}
          </Button>
        </div>
      );
    }
    // 三段中文合成一份(画面描述就是输入框本身)。
    const h3All = { ...h3Fields, main: inputValue.trim() };
    const h3Ctx = h3AlignContext || { tabKey: mode };
    // 有结构化优化结果时发的是它,没有就发输入框原文——所见即所发。
    //
    // H3 多一条兜底:没优化过就按字段名本地拼一份再发,而不是把一句中文散文裸发上去
    // (改动前的行为)。引擎对 prompt 完全不解析,裸发不会报错、只会默默出差档;而
    // 优化按钮可能根本不存在(运营没配优化模型时 usePromptOptimize 不渲染它),所以
    // 这条路必须自己站得住,不能指望用户先去点优化。
    const outgoing = sections
      ? joinH3Prompt(sections)
      : isH3
        ? buildLocalH3Prompt(h3All, h3Ctx)
        : inputValue.trim();
    // 视频配乐(dub)提示词可选:空文本=让模型按画面自由配环境音(hook/网关/引擎
    // 全链路已放行)。缺视频仍由 blockSend 里的 missingRequiredImage 拦住。
    //
    // H3 未优化时**不能拿 outgoing 判空**:本地兜底会在没写画面描述时也拼出非空文本
    // ——传了首帧就有一句自动补的对齐指令,只填了音景就有一段 overall_soundscape。
    // 那种提示词缺主字段,发出去必然是废的,而且照样扣额度跑几分钟。故这一支单独按
    // 「画面描述非空」判。优化过(sections 非空)则不必:parseH3Prompt 判空的依据就是
    // 有没有主字段,降级路径拼出来的那份也是以主字段打头。
    const canSend =
      !blockSend &&
      !optimizing &&
      (isH3 && !sections
        ? inputValue.trim().length > 0
        : outgoing.length > 0 || isDub);
    const doSend = () => {
      if (!canSend) return;
      onSend(outgoing);
      setInputValue('');
      setH3Fields({});
      setSections(null);
    };
    return (
      <div className='p-2 sm:p-4'>
        {turnLimitReached && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {t('本轮对话已达生成上限，请点击右侧「新对话」继续')}
          </Typography.Text>
        )}
        {/* 一键示例:纯文本(仅填输入框)或结构化对象({label,prompt,params,files}——
            同时预置首帧/参考图/驱动音等文件)。单行等宽排列,超长 CSS 截断。 */}
        {hasPresets && (
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
                    // 换示例 = 从头来过。上一次的优化结果必须一并清掉:outgoing 优先
                    // 取 sections,不清的话点了新示例、发出去的还是上一条优化结果,
                    // 而界面上看不出任何异样。
                    setSections(null);
                    // 另外两段:H3 示例自带就填上,否则清空(留着会串到新示例上)。
                    // 整体赋值而不是 merge —— merge 会让上一条示例的音景活到这一条。
                    setH3Fields(isObj && ex.h3 ? { ...ex.h3 } : {});
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
        <PromptOptimizeButton
          category={category}
          tabKey={mode}
          // H3 送去优化的是三段中文按字段名拼好的一份,而不是光秃秃一句话:优化模型
          // 这样才知道哪句归哪段,音景与配乐也就不会被它按自己的想象重写一遍。
          // 画面描述为空时传空串,以便沿用「先写个大概方向」那句提示。
          value={
            isH3
              ? inputValue.trim()
                ? buildLocalH3Prompt(h3All, h3Ctx)
                : ''
              : inputValue
          }
          onChange={(out) => {
            // 能切出 H3 的分段结构就折叠展示(输入框保留原想法,方便改一改再优化
            // 一次)。模型偶尔不按格式返回是常态,降级路径必须存在。
            const parsed = parseH3Prompt(out);
            if (parsed) {
              setSections(parsed);
              return;
            }
            // 降级:非 H3 玩法退回原来的行为——把原文回填输入框。
            //
            // H3 不能这么做:那个框现在装的是「画面描述」的中文原文,灌英文进去等于
            // 把用户写的中文连同下方的中英对照一起冲掉。改为把整段结果当成正文那一段
            // 收进来,中文原封不动留着,用户照样能改、能重新优化。
            if (!isH3) {
              setSections(null);
              setInputValue(out);
              return;
            }
            // 另外两段中文要跟着带上,否则一次「优化没按格式返回」就把用户填的音景与
            // 配乐一起吞掉了 —— 提交走的是 sections,不再看 h3Fields。
            // 但结果里已经自带该字段名时不重复追加:parseH3Prompt 判空的依据是缺主
            // 字段,「有音景、没主字段」正是常见的失败形态,再追加就成了同一字段出现
            // 两次。
            setSections([
              { key: h3MainKey(mode), value: out, sep: ' ' },
              ...['overall_soundscape', 'non_diegetic_music']
                .filter(
                  (key) =>
                    (h3Fields[key] || '').trim() && !h3HasField(out, key),
                )
                .map((key) => ({
                  key,
                  value: h3Fields[key].trim(),
                  sep: ' ',
                })),
            ]);
          }}
          disabled={generating}
          onOptimizingChange={setOptimizing}
          engine={optimizeEngine}
          optimizeContext={optimizeContext}
        />
        {isH3 && <H3MainFieldLabel />}
        <div className='relative'>
          <TextArea
            value={inputValue}
            onChange={setInputValue}
            placeholder={t(
              isDub
                ? '描述画面里什么在发声:动作、接触材质、环境音(只做音效,不生成音乐与台词)'
                : isH3
                  ? '用大白话写画面:谁、在哪、做什么、怎么运镜;有台词就用引号原样写'
                  : '请输入视频生成提示词',
            )}
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
            className='!rounded-full !bg-purple-500 hover:!bg-purple-600'
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
        {isH3 && (
          <H3PromptFields
            values={h3Fields}
            onChange={(key, v) =>
              setH3Fields((prev) => ({ ...prev, [key]: v }))
            }
            disabled={generating}
          />
        )}
        <OptimizedPromptSections
          sections={sections}
          // 中英对照:每段英文上面摆用户当初填的那段中文。画面描述在 base 与 ref 两套
          // 格式里字段名不同(h3MainKey),但界面上是同一段。
          sourceByKey={
            isH3
              ? {
                  [h3MainKey(mode)]: inputValue,
                  overall_soundscape: h3Fields.overall_soundscape,
                  non_diegetic_music: h3Fields.non_diegetic_music,
                }
              : null
          }
          onChange={setSections}
          onDiscard={() => setSections(null)}
          disabled={generating}
        />
      </div>
    );
  }, [
    generating,
    turnLimitReached,
    missingRequiredImage,
    hasPresets,
    presets,
    onApplyExample,
    isSR,
    isDub,
    inputValue,
    optimizing,
    sections,
    isH3,
    h3Fields,
    h3AlignContext,
    onSend,
    category,
    mode,
    optimizeEngine,
    optimizeContext,
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
          placeholder={t('请输入视频生成提示词')}
          style={{ height: '100%' }}
        />
      </div>
    </Card>
  );
};

export default VideoChatArea;
