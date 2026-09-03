import React, { useState, useMemo, useCallback, useContext } from 'react';
import { Card, Chat, Button, Typography, TextArea } from '@douyinfe/semi-ui';
import { Copy, Download, RefreshCw, Send } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  API,
  showSuccess,
  showError,
  getLogo,
  stringToColor,
} from '../../helpers';
import {
  IMAGE_GEN_STATUS,
  IMAGE_API_ENDPOINTS,
  IMAGE_PROMPT_PRESETS,
} from '../../constants/imagePlayground.constants';
import { UserContext } from '../../context/User';
import { formatQueueHint } from '../../helpers/queueHint';
import { blockChatDrag } from '../playground/blockChatDrag';
import PromptOptimizeButton from '../playground/PromptOptimizeButton';
import ImagePreviewModal from './ImagePreviewModal';

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

// 取图片字节：base64 / blob:(IDB 恢复)直接取；远程 url 经后端代理取,绕开 CDN 的 CORS
// 限制。blob: 必须走本地 fetch——发给后端代理必失败(§4.4)。
const fetchImageBlob = async (src) => {
  if (src.startsWith('data:') || src.startsWith('blob:')) {
    const resp = await fetch(src);
    return resp.blob();
  }
  const res = await API.get(
    `${IMAGE_API_ENDPOINTS.IMAGE_PROXY}?url=${encodeURIComponent(src)}`,
    { responseType: 'blob', skipErrorHandler: true },
  );
  return res.data;
};

const copyImage = async (src, t) => {
  try {
    if (!navigator.clipboard || !window.ClipboardItem) {
      throw new Error('clipboard unsupported');
    }
    const blob = await fetchImageBlob(src);
    await navigator.clipboard.write([
      new window.ClipboardItem({ [blob.type || 'image/png']: blob }),
    ]);
    showSuccess(t('图片已复制'));
  } catch (e) {
    showError(t('复制失败'));
  }
};

const downloadImage = async (src, t) => {
  try {
    const blob = await fetchImageBlob(src);
    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = `image-${Date.now()}.png`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  } catch (e) {
    showError(t('下载失败'));
  }
};

const ImageChatArea = ({
  messages,
  generating,
  interruptible,
  turnLimitReached = false,
  missingRequiredImage = false,
  mode = 'text2image',
  // 选中的模型名：「AI 优化提示词」按它取模型级的系统提示词改写（没写则跟随 tab）。
  selectedModel = '',
  // 所选模型的引擎族：决定内置模板走哪一份（SenseNova-U1.5 的口径与通用模板差别很大，
  // 见 promptOptimize.constants.js）。留空即通用模板。
  optimizeEngine = '',
  // 图生图底图：一并发给「AI 优化提示词」的模型，让它看着图改写而不是靠文字猜。
  // 文生图恒为空。顺序即模型认的图片编号，与左侧缩略图上的序号角标一一对应。
  optimizeImages,
  // 本次请求的既成事实（目标画幅、底图张数与编号），拼在系统提示词末尾。
  optimizeContext = '',
  showPresets = false,
  onSend,
  onRegenerate,
  onClear,
  onRefetch,
}) => {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const [preview, setPreview] = useState({ visible: false, src: '' });
  // 受控输入框:预设按钮直接 setInputValue,发送后清空(缺图/上限时不清空,提示词不丢)。
  const [inputValue, setInputValue] = useState('');
  // AI 优化提示词在途:此刻允许发送等于把没优化的原文发出去,故一并灰掉发送按钮。
  const [optimizing, setOptimizing] = useState(false);

  const roleConfig = useMemo(
    () => ({
      user: {
        name: userState?.user?.username || 'User',
        avatar: genUserAvatar(userState?.user?.username),
      },
      assistant: { name: t('图片模型'), avatar: getLogo() },
      system: { name: 'System', avatar: getLogo() },
    }),
    [userState, t],
  );

  // 内部消息 -> Semi Chat 所需结构
  const chats = useMemo(() => {
    if (!messages.length) {
      return [
        {
          role: 'assistant',
          id: WELCOME_ID,
          createAt: 1,
          content: t('欢迎使用 AI 图像生成，请在下方输入您的提示词'),
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
      const status =
        m.status === IMAGE_GEN_STATUS.PENDING
          ? 'loading'
          : m.status === IMAGE_GEN_STATUS.FAILED
            ? 'error'
            : 'complete';
      return {
        role: 'assistant',
        id: m.id,
        createAt: parseTs(m.id, i + 1),
        status,
        content:
          m.status === IMAGE_GEN_STATUS.FAILED
            ? m.error || t('图片生成失败')
            : '',
      };
    });
  }, [messages, t]);

  const byId = useMemo(
    () => new Map(messages.map((m) => [m.id, m])),
    [messages],
  );

  // 自定义消息内容：成功的助手消息渲染图片 + 操作按钮
  const renderChatBoxContent = useCallback(
    ({ message, defaultContent }) => {
      const m = byId.get(message.id);
      if (!m || m.role === 'user') return defaultContent;
      // 异步多候选是 N 个独立任务，先出来的先显示 —— 等齐了再一次性渲染，
      // 会让先完成的那几张白白干等最慢的一张。
      const partial =
        m.status === IMAGE_GEN_STATUS.PENDING && (m.images || []).length > 0;
      // 轮询撞上限：任务还在服务端跑，给一个用原 taskId 续查的入口，
      // 而不是让用户重发（那会再扣一次费）。
      //
      // 抽成片段是因为它有两个落点：一张都没出来时单独成块；已经出了几张时要跟在
      // 图片区标题下面。只放在「无图」那一支的话，「部分完成 + 超时」会既没有按钮、
      // 又停着不动，界面上还写着「其余生成中…」—— 停了却说在生成，最难排查。
      const timedOutHint =
        m.status === IMAGE_GEN_STATUS.PENDING && m.pollTimedOut ? (
          <div className='mb-2'>
            <Typography.Text type='tertiary' className='text-sm block mb-1'>
              {t('生成时间较长，任务仍在后台处理')}
            </Typography.Text>
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              onClick={() => onRefetch && onRefetch(m.id)}
              className='!text-gray-500'
            >
              {t('继续获取')}
            </Button>
          </div>
        ) : null;

      // 排队回显：一张都没出来时才有意义 —— 已经在出图了就不是在排队。
      // 门面说不准（非自建渠道、派发中）时为 null，退回 defaultContent 的通用态。
      const queueHint =
        m.status === IMAGE_GEN_STATUS.PENDING && !m.pollTimedOut
          ? formatQueueHint(m.queueAhead, m.queueEtaSeconds, t)
          : null;

      if (m.status !== IMAGE_GEN_STATUS.SUCCESS && !partial) {
        if (timedOutHint)
          return <div className='inline-block'>{timedOutHint}</div>;
        if (queueHint)
          return (
            <div className='inline-block'>
              <Typography.Text type='tertiary' className='text-sm'>
                {queueHint}
              </Typography.Text>
            </div>
          );
        return defaultContent;
      }
      // base64 图片不落盘，刷新后历史里这类图已不在
      if ((!m.images || m.images.length === 0) && m.imagesNotPersisted) {
        return (
          <Typography.Text type='tertiary' className='text-sm'>
            {t('图片已过期或本地缓存被清理，请重新生成')}
          </Typography.Text>
        );
      }
      return (
        <div className='inline-block'>
          {/* partial 只可能出现在**存量会话**里:改造后每条消息只挂一个任务,
              图出来的同时状态就转 SUCCESS,不存在「出了一半」的中间态。老消息一条挂
              N 个任务,这句仍然是它们的正确描述,所以留着。
              batchTotal 是新消息的口径:多张时每条各自报「第 n/N 张」,与视频侧一致。 */}
          <Typography.Text className='text-sm text-gray-600 block mb-2'>
            {partial
              ? t('已出 {{done}}/{{total}} 张，其余生成中…', {
                  done: (m.images || []).length,
                  total: (m.imageTasks || []).length || (m.images || []).length,
                })
              : m.batchTotal > 1
                ? t('图像已生成（第 {{i}}/{{n}} 张）', {
                    i: (m.batchIndex ?? 0) + 1,
                    n: m.batchTotal,
                  })
                : t('图像已生成')}
          </Typography.Text>
          {timedOutHint}
          {/* 多张时每张各自带 seed 与复制/下载。
              **复制/下载必须按张**:原来这两个按钮恒取 images[0],单张时看不出问题,
              多张时后几张就没法单独拿走 —— 而"多生成几张让用户挑"的下一步正是
              把选中的那张拿走。seed 同理:显示出来用户才能拿它复现、微调,否则
              "挑中了却回不去"。单张且没显式给 seed 时 imageSeeds[idx] 为空,不显示。 */}
          <div className='flex flex-wrap gap-3'>
            {(m.images || []).map((src, idx) => {
              const seed = (m.imageSeeds || [])[idx];
              return (
                <div key={idx} className='flex flex-col gap-1'>
                  <img
                    src={src}
                    alt='generated'
                    onClick={() => setPreview({ visible: true, src })}
                    className='rounded-lg cursor-zoom-in object-cover'
                    style={{ maxWidth: 360, maxHeight: 360 }}
                  />
                  <div className='flex items-center gap-1'>
                    {seed != null && (
                      <Typography.Text
                        type='tertiary'
                        className='text-xs mr-1'
                        copyable={{ content: String(seed) }}
                      >
                        {t('种子 {{seed}}', { seed })}
                      </Typography.Text>
                    )}
                    <Button
                      theme='borderless'
                      type='tertiary'
                      size='small'
                      icon={<Copy size={14} />}
                      onClick={() => copyImage(src, t)}
                      className='!text-gray-500'
                    />
                    <Button
                      theme='borderless'
                      type='tertiary'
                      size='small'
                      icon={<Download size={14} />}
                      onClick={() => downloadImage(src, t)}
                      className='!text-gray-500'
                    />
                  </div>
                </div>
              );
            })}
          </div>
          <div className='flex items-center gap-1 mt-2'>
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
    },
    [byId, generating, onRegenerate, t],
  );

  // 自定义输入区:预设提示词按钮 + 受控 TextArea + 右下角圆形发送按钮。
  const renderInputArea = useCallback(() => {
    // 缺必填底图/生成中/达上限时置灰,回车与点击均不发送,提示词不丢。
    const blockSend = generating || turnLimitReached || missingRequiredImage;
    const canSend = !blockSend && !optimizing && inputValue.trim().length > 0;
    const doSend = () => {
      if (!canSend) return;
      onSend(inputValue.trim());
      setInputValue('');
    };
    return (
      <div className='p-2 sm:p-4'>
        {interruptible && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {t(
              '图片生成中，请勿刷新页面或切换到其他功能页，否则本次任务将中断，需重新生成',
            )}
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
        {/* 预设提示词(仅文生图):单行等宽排列,超长 CSS 截断;点击清空当前输入并填入 */}
        {showPresets && (
          <div className='flex gap-2 mb-2 overflow-hidden'>
            {IMAGE_PROMPT_PRESETS.map((p, i) => (
              <button
                key={i}
                type='button'
                title={p}
                onClick={() => setInputValue(p)}
                className='flex-1 min-w-0 truncate text-xs text-gray-600 bg-gray-100 hover:bg-gray-200 rounded-full px-3 py-1.5 transition-colors'
              >
                {p}
              </button>
            ))}
          </div>
        )}
        <PromptOptimizeButton
          category='image'
          tabKey={mode}
          model={selectedModel}
          engine={optimizeEngine}
          optimizeContext={optimizeContext}
          images={optimizeImages}
          value={inputValue}
          onChange={setInputValue}
          disabled={generating}
          onOptimizingChange={setOptimizing}
        />
        <div className='relative'>
          <TextArea
            value={inputValue}
            onChange={setInputValue}
            placeholder={t('请输入图片生成提示词')}
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
      </div>
    );
    // renderInputArea 是 Semi Chat 的渲染回调:deps 不变就返回上一个闭包,
    // 里面的 props 全是旧值。所以凡是**透传给 PromptOptimizeButton 的 prop 都必须
    // 进 deps**——漏一个就是"换了模型/换了底图,优化按钮还按上一份在发"，不报错。
    // selectedModel / optimizeEngine 是补的既有遗漏(与本次改动同一类,一并修)。
  }, [
    generating,
    turnLimitReached,
    missingRequiredImage,
    inputValue,
    optimizing,
    onSend,
    mode,
    selectedModel,
    optimizeEngine,
    optimizeImages,
    optimizeContext,
    showPresets,
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
          placeholder={t('请输入图片生成提示词')}
          style={{ height: '100%' }}
        />
      </div>
      <ImagePreviewModal
        visible={preview.visible}
        src={preview.src}
        onClose={() => setPreview({ visible: false, src: '' })}
      />
    </Card>
  );
};

export default ImageChatArea;
