import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, CapsuleTabs, Empty, NavBar, TextArea } from 'antd-mobile';

import { useMusicGeneration } from '@classic/hooks/musicPlayground/useMusicGeneration';
import {} from '@classic/constants/musicPlayground.constants';

import AsyncTaskBubble from '../components/gen/AsyncTaskBubble';
import { useVisibleModes, useDesktopOnlyHint } from '../hooks/useVisibleModes';
import { useAutoOpenLatest } from '../hooks/useAutoOpenLatest';
import ConfigBar from '../components/gen/ConfigBar';
import ConfigCollapse from '../components/gen/ConfigCollapse';
import ConversationBar from '../components/gen/ConversationBar';
import MediaBar from '../components/gen/MediaBar';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import ShareBar from '../components/gen/ShareBar';

const MusicBody = ({ mode }) => {
  const {
    inputs,
    handleInputChange,
    groups,
    models,
    messages,
    generating,
    locked,
    turnLimitReached,
    missingRequiredAudio,
    engine,
    needsAudio,
    needsText,
    refAudioMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    conversations,
    currentConvId,
    openHistoryItem,
    deleteHistoryItem,
    clearHistory,
  } = useMusicGeneration(mode);

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  const [showLyrics, setShowLyrics] = useState(false);
  // 歌词只有 ACE-Step 系（文生音乐/改编/重绘）认。
  const isAceStep = engine === 'acestep';
  // 锁定态（选中了某条会话）参数与素材都改不动，见 useMusicGeneration 的 handleInputChange。
  const editDisabled = generating || locked;

  const mediaSlots = [
    needsAudio && {
      type: 'single',
      key: 'audioData',
      kind: 'audio',
      label: mode === 'cover' ? '参考音频' : '源音频',
      required: true,
      maxMB: refAudioMaxMB,
      value: inputs.audioData,
      name: inputs.audioName,
      onChange: (v, name) => {
        handleInputChange('audioData', v || '');
        handleInputChange('audioName', v ? name : '');
      },
    },
  ];

  const renderAssistant = (m) => (
    <div>
      {m.translatedText && (
        <div
          style={{
            fontSize: 12,
            color: 'var(--adm-color-weak)',
            marginBottom: 6,
          }}
        >
          译文：{m.translatedText}
        </div>
      )}
      <AsyncTaskBubble
        m={m}
        doneStatus='completed'
        resultUrl={m.musicUrl}
        renderResult={(url) => (
          <div>
            <audio controls src={url} style={{ width: '100%' }} />
            <ShareBar
              url={url}
              filename={`music-${m.taskId || m.id}.mp3`}
              taskId={m.taskId}
            />
          </div>
        )}
        onRetry={() => regenerate(m.prompt)}
        onRefetch={() => refetch(m.id, m.taskId)}
      />
    </div>
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <ConfigCollapse
        locked={locked}
        title={inputs.model}
        slots={mediaSlots}
        onNew={newConversation}
      >
        <ConfigBar
          disabled={editDisabled}
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
            // 手机端不摆时长下拉：文生音乐未填歌词时走 sample_mode，引擎的
            // llm_generation_inputs.py 会用 LM 自己推的时长无条件覆盖下发值，摆出来
            // 就是个点了不生效的假开关（同视频页 s2v 的处理）。要指定时长去网页端。
          ]}
        />
        <MediaBar
          slots={mediaSlots}
          disabled={editDisabled}
          readOnly={locked}
        />
      </ConfigCollapse>
      <ConversationBar
        conversations={conversations}
        currentConvId={currentConvId}
        onOpen={openHistoryItem}
        onDelete={deleteHistoryItem}
        onClear={clearHistory}
      />
      <div style={{ flex: 1, overflowY: 'auto' }}>
        <MessageFeed
          messages={messages}
          renderAssistant={renderAssistant}
          empty={
            needsAudio
              ? `上传${mode === 'cover' ? '参考' : '源'}音频，再描述想要的改动`
              : '描述想要的音乐风格，可选填歌词'
          }
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        optimizeCategory='music'
        optimizeTab={mode}
        disabled={turnLimitReached || missingRequiredAudio}
        allowEmpty={!needsText}
        placeholder={
          missingRequiredAudio
            ? '请先上传音频'
            : needsAudio
              ? '描述想要的改动…'
              : '描述音乐风格…'
        }
        extra={
          isAceStep ? (
            <div style={{ marginBottom: 8 }}>
              <Button
                size='mini'
                fill='none'
                onClick={() => setShowLyrics(!showLyrics)}
              >
                {showLyrics ? '收起歌词' : '填写歌词（可选）'}
              </Button>
              {showLyrics && (
                <div
                  style={{
                    background: 'var(--adm-color-fill-content, #f5f5f5)',
                    borderRadius: 8,
                    padding: '6px 10px',
                    marginTop: 4,
                  }}
                >
                  <TextArea
                    placeholder='歌词留空则由引擎自动生成'
                    value={inputs.lyrics}
                    onChange={(v) => handleInputChange('lyrics', v)}
                    disabled={editDisabled}
                    rows={2}
                    autoSize={{ minRows: 2, maxRows: 6 }}
                  />
                </div>
              )}
            </div>
          ) : null
        }
      />
    </div>
  );
};

const Music = () => {
  const navigate = useNavigate();
  const modes = useVisibleModes('music');
  const desktopOnlyHint = useDesktopOnlyHint('music');
  const [mode, setMode] = useState(modes[0]?.key || 't2m');
  useEffect(() => {
    if (modes.length && !modes.some((m) => m.key === mode))
      setMode(modes[0].key);
  }, [modes, mode]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar onBack={() => navigate(-1)}>音乐音效</NavBar>
      {modes.length === 0 ? (
        <Empty style={{ padding: 32 }} description='当前体验区暂未开放' />
      ) : (
        <>
          <CapsuleTabs activeKey={mode} onChange={setMode}>
            {modes.map((m) => (
              <CapsuleTabs.Tab key={m.key} title={m.title} />
            ))}
          </CapsuleTabs>
          {desktopOnlyHint && (
            <div className='m-desktop-hint'>{desktopOnlyHint}</div>
          )}
          <div style={{ flex: 1, minHeight: 0 }}>
            <MusicBody key={mode} mode={mode} />
          </div>
        </>
      )}
    </div>
  );
};

export default Music;
