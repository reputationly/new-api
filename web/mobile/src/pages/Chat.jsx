import React, { useContext } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Collapse, NavBar } from 'antd-mobile';

import { UserContext } from '@classic/context/User';
import { usePlaygroundState } from '@classic/hooks/playground/usePlaygroundState';
import { useApiRequest } from '@classic/hooks/playground/useApiRequest';
import { useDataLoader } from '@classic/hooks/playground/useDataLoader';
import {
  MESSAGE_ROLES,
  MESSAGE_STATUS,
} from '@classic/constants/playground.constants';
import { buildApiPayload } from '@classic/helpers/api';

import ConfigBar from '../components/gen/ConfigBar';
import MarkdownMessage from '../components/gen/MarkdownMessage';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import {
  createMessage,
  createLoadingAssistantMessage,
  getTextContent,
  processIncompleteThinkTags,
} from '../shims/classic-utils';

const Chat = () => {
  const navigate = useNavigate();
  const [userState] = useContext(UserContext);

  const state = usePlaygroundState();
  const {
    inputs,
    parameterEnabled,
    message,
    setMessage,
    setModels,
    setGroups,
    setModelEndpointTypes,
    setDebugData,
    setActiveDebugTab,
    sseSourceRef,
    saveMessagesImmediately,
    handleInputChange,
    models,
    groups,
  } = state;

  const { sendRequest, onStopGenerator } = useApiRequest(
    setMessage,
    setDebugData,
    setActiveDebugTab,
    sseSourceRef,
    saveMessagesImmediately,
  );

  useDataLoader(
    userState,
    inputs,
    handleInputChange,
    setModels,
    setGroups,
    setModelEndpointTypes,
  );

  const lastMessage = message[message.length - 1];
  const generating =
    lastMessage?.role === MESSAGE_ROLES.ASSISTANT &&
    (lastMessage.status === MESSAGE_STATUS.LOADING ||
      lastMessage.status === MESSAGE_STATUS.INCOMPLETE);

  const handleSend = (content) => {
    if (generating) return;
    const userMessage = createMessage(MESSAGE_ROLES.USER, content);
    const loadingMessage = createLoadingAssistantMessage();
    setMessage((prev) => {
      const newMessages = [...prev, userMessage];
      const payload = buildApiPayload(
        newMessages,
        null,
        inputs,
        parameterEnabled,
      );
      sendRequest(payload, inputs.stream);
      const withLoading = [...newMessages, loadingMessage];
      setTimeout(() => saveMessagesImmediately(withLoading), 0);
      return withLoading;
    });
  };

  const handleClear = () => {
    // 与 classic handleClearMessages 一致：清空并立即持久化空列表，
    // 否则重进对话页会重新加载已存的旧消息（原来只 setMessage 未落盘）。
    setMessage([]);
    setTimeout(() => saveMessagesImmediately([]), 0);
  };

  const renderAssistant = (m) => {
    // 模型吐思考有两条路:独立的 reasoning_content 字段,和正文里的 <think> 标签。
    // 流式链路只搬前者——useApiRequest 的 streamMessageUpdate/completeMessage 全程不碰
    // <think>,只有手动停止和刷新修复才会解析。所以正文内联的那种必须在渲染时抽,
    // 与 classic MessageContent 同策略;漏了就会像多轮对话里那样裸露一段 <think>。
    // 用 Incomplete 版是为了流式期间半截未闭合的 <think> 也能进折叠卡而不是甩在正文。
    const { content: text, reasoningContent } = processIncompleteThinkTags(
      getTextContent(m),
      m.reasoningContent || '',
    );
    // 思考抽走后正文可能暂时为空:<think> 闭合前所有 token 都在思考里,正文一片空白
    // 看着像卡死。LOADING(等首包)和 INCOMPLETE(思考流式中)都要给占位,只判 LOADING
    // 会漏掉后者——正文有内容时 text 自然顶掉占位,不会误显示。
    const pending =
      m.status === MESSAGE_STATUS.LOADING ||
      m.status === MESSAGE_STATUS.INCOMPLETE;
    return (
      <div>
        {reasoningContent && (
          <Collapse defaultActiveKey={[]} style={{ marginBottom: 8 }}>
            <Collapse.Panel key='think' title='思考过程'>
              <div
                style={{
                  fontSize: 13,
                  color: 'var(--adm-color-weak)',
                  whiteSpace: 'pre-wrap',
                }}
              >
                {reasoningContent}
              </div>
            </Collapse.Panel>
          </Collapse>
        )}
        {/* 落到终态才渲染 markdown,流式期间维持 pre-wrap 原样输出:一次绕开逐 token
            重新 parse 的性能开销和半截语法造成的跳变闪烁,详见 MarkdownMessage 顶部注释。 */}
        {text && m.status === MESSAGE_STATUS.COMPLETE ? (
          <MarkdownMessage>{text}</MarkdownMessage>
        ) : (
          <div style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
            {text || (pending ? '思考中…' : '')}
          </div>
        )}
        {m.status === MESSAGE_STATUS.ERROR && (
          <div style={{ color: 'var(--adm-color-danger)', marginTop: 4 }}>
            请求出错，请重试
          </div>
        )}
      </div>
    );
  };

  const visibleMessages = message.filter(
    (m) => m.role !== MESSAGE_ROLES.SYSTEM,
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar
        onBack={() => navigate(-1)}
        right={
          visibleMessages.length > 0 && (
            <Button size='mini' fill='none' onClick={handleClear}>
              清空
            </Button>
          )
        }
      >
        对话
      </NavBar>
      <ConfigBar
        disabled={generating}
        fields={[
          {
            key: 'group',
            label: '分组',
            value: inputs.group,
            options: groups,
            onChange: (v) => handleInputChange('group', v),
          },
          {
            key: 'model',
            label: '模型',
            value: inputs.model,
            options: models,
            onChange: (v) => handleInputChange('model', v),
          },
        ]}
      />
      <div style={{ flex: 1, overflowY: 'auto' }}>
        <MessageFeed
          messages={visibleMessages}
          renderAssistant={renderAssistant}
          empty='输入内容开始对话'
        />
      </div>
      {generating && (
        <div style={{ textAlign: 'center', padding: 4 }}>
          <Button size='mini' fill='outline' onClick={onStopGenerator}>
            停止生成
          </Button>
        </div>
      )}
      <PromptBar
        onSend={handleSend}
        generating={false}
        disabled={generating}
        placeholder='输入内容…'
      />
    </div>
  );
};

export default Chat;
