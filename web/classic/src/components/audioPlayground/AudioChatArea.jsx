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
import {
  AUDIO_STATUS,
  AUDIO_PROMPT_PRESETS,
} from '../../constants/audioPlayground.constants';

// 语音合成对话区,镜像 VideoChatArea:成品渲染 <audio> 播放器 + 下载 wav。

const presetLabel = (s) => {
  const v = (s || '').trim();
  return v.length > 22 ? `${v.slice(0, 22)}…` : v;
};

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

const downloadAudio = async (url, t) => {
  try {
    const resp = await fetch(url, { credentials: 'include' });
    const blob = await resp.blob();
    const blobUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = `audio-${Date.now()}.wav`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(blobUrl);
  } catch (e) {
    showError(t('下载失败'));
  }
};

// 生成中:三阶段 + 进度(语音任务通常无百分比,走 Spin 文案)。
const AudioProgress = ({ status, progress, t }) => {
  const current = status === AUDIO_STATUS.QUEUED ? 0 : 1;
  const stages = [t('排队中'), t('合成中'), t('完成')];
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
            {status === AUDIO_STATUS.QUEUED ? t('任务排队中…') : t('合成中…')}
          </div>
        )}
      </div>
    </div>
  );
};

const AudioChatArea = ({
  messages,
  generating,
  turnLimitReached = false,
  missingRequiredVoice = false,
  onSend,
  onRegenerate,
  onRefetch,
  onClear,
}) => {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const [inputValue, setInputValue] = useState('');

  const roleConfig = useMemo(
    () => ({
      user: {
        name: userState?.user?.username || 'User',
        avatar: genUserAvatar(userState?.user?.username),
      },
      assistant: { name: t('语音模型'), avatar: getLogo() },
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
          content: t(
            '欢迎使用 AI 语音合成,请在左侧选择参考音色,并在下方输入要合成的文本',
          ),
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
        m.status === AUDIO_STATUS.COMPLETED ||
        m.status === AUDIO_STATUS.FAILED ||
        m.status === AUDIO_STATUS.CANCELED;
      return {
        role: 'assistant',
        id: m.id,
        createAt: parseTs(m.id, i + 1),
        status: done
          ? m.status === AUDIO_STATUS.FAILED
            ? 'error'
            : 'complete'
          : 'loading',
        content:
          m.status === AUDIO_STATUS.FAILED ? m.error || t('语音合成失败') : '',
      };
    });
  }, [messages, t]);

  const byId = useMemo(
    () => new Map(messages.map((m) => [m.id, m])),
    [messages],
  );

  const renderChatBoxContent = useCallback(
    ({ message, defaultContent }) => {
      const m = byId.get(message.id);
      if (!m || m.role === 'user') return defaultContent;
      if (m.status === AUDIO_STATUS.COMPLETED && m.audioUrl) {
        return (
          <div className='inline-block' style={{ minWidth: 320 }}>
            <Typography.Text className='text-sm text-gray-600 block mb-2'>
              {t('语音已合成')}
            </Typography.Text>
            <audio
              src={m.audioUrl}
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
                onClick={() => downloadAudio(m.audioUrl, t)}
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
      if (m.status === AUDIO_STATUS.FAILED) {
        return (
          <div className='inline-block'>
            <Typography.Text type='danger' className='text-sm block mb-1'>
              {m.error || t('语音合成失败')}
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
      if (m.status === AUDIO_STATUS.CANCELED) {
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
              {t('合成时间较长，任务仍在后台处理')}
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
      return <AudioProgress status={m.status} progress={m.progress} t={t} />;
    },
    [byId, generating, onRegenerate, onRefetch, t],
  );

  const renderInputArea = useCallback(() => {
    // 缺参考音色/生成中/达上限时置灰;回车与点击均不发送,文本不丢。
    const blockSend = generating || turnLimitReached || missingRequiredVoice;
    const canSend = !blockSend && inputValue.trim().length > 0;
    const doSend = () => {
      if (!canSend) return;
      onSend(inputValue.trim());
      setInputValue('');
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
        {missingRequiredVoice && (
          <Typography.Text
            type='warning'
            className='text-xs block mb-2 text-center'
          >
            {t('请先在左侧选择预置音色或上传参考音频')}
          </Typography.Text>
        )}
        <div className='flex flex-wrap gap-2 mb-2'>
          {AUDIO_PROMPT_PRESETS.map((p, i) => (
            <button
              key={i}
              type='button'
              title={p}
              onClick={() => setInputValue(p)}
              className='text-xs text-gray-600 bg-gray-100 hover:bg-gray-200 rounded-full px-3 py-1.5 truncate max-w-[220px] transition-colors'
            >
              {presetLabel(p)}
            </button>
          ))}
        </div>
        <div className='relative'>
          <TextArea
            value={inputValue}
            onChange={setInputValue}
            placeholder={t('请输入要合成的文本')}
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
    missingRequiredVoice,
    inputValue,
    onSend,
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
        placeholder={t('请输入要合成的文本')}
        style={{ height: '100%' }}
      />
    </Card>
  );
};

export default AudioChatArea;
