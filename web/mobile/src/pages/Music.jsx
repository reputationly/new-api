import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, CapsuleTabs, Empty, NavBar, TextArea } from 'antd-mobile';

import { useMusicGeneration } from '@classic/hooks/musicPlayground/useMusicGeneration';
import {
  MUSIC_DURATIONS,
  MUSIC_SVS_CONTROLS,
  MUSIC_SVS_LANGUAGES,
} from '@classic/constants/musicPlayground.constants';

import AsyncTaskBubble from '../components/gen/AsyncTaskBubble';
import { useVisibleModes } from '../hooks/useVisibleModes';
import { useAutoOpenLatest } from '../hooks/useAutoOpenLatest';
import ConfigBar from '../components/gen/ConfigBar';
import ConfigCollapse from '../components/gen/ConfigCollapse';
import ConversationBar from '../components/gen/ConversationBar';
import MediaBar, { MOBILE_MAX_VIDEO_MB } from '../components/gen/MediaBar';
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
    missingRequiredVideo,
    engine,
    needsAudio,
    needsVideo,
    needsDualAudio,
    needsText,
    refAudioMaxMB,
    videoMaxMB,
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
  const isT2M = mode === 't2m';
  // 歌词只有 ACE-Step 系（文生音乐/改编/重绘）认，音效与歌声合成不下发。
  const isAceStep = engine === 'acestep';
  const mobileVideoMaxMB = videoMaxMB
    ? Math.min(videoMaxMB, MOBILE_MAX_VIDEO_MB)
    : MOBILE_MAX_VIDEO_MB;
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
    needsVideo && {
      type: 'single',
      key: 'videoData',
      kind: 'video',
      label: '源视频',
      required: true,
      maxMB: mobileVideoMaxMB,
      value: inputs.videoData,
      name: inputs.videoName,
      onChange: (v, name) => {
        handleInputChange('videoData', v || '');
        handleInputChange('videoName', v ? name : '');
      },
    },
    needsDualAudio && {
      type: 'single',
      key: 'promptAudioData',
      kind: 'audio',
      label: '音色参考（人声）',
      required: true,
      maxMB: refAudioMaxMB,
      value: inputs.promptAudioData,
      name: inputs.promptAudioName,
      onChange: (v, name) => {
        handleInputChange('promptAudioData', v || '');
        handleInputChange('promptAudioName', v ? name : '');
      },
    },
    needsDualAudio && {
      type: 'single',
      key: 'targetAudioData',
      kind: 'audio',
      label: '目标曲/伴奏',
      required: true,
      maxMB: refAudioMaxMB,
      value: inputs.targetAudioData,
      name: inputs.targetAudioName,
      onChange: (v, name) => {
        handleInputChange('targetAudioData', v || '');
        handleInputChange('targetAudioName', v ? name : '');
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
            ...(isT2M
              ? [
                  {
                    key: 'duration',
                    label: '时长',
                    value: inputs.duration,
                    options: MUSIC_DURATIONS.map((d) => ({
                      label: d ? `${d} 秒` : '默认',
                      value: d,
                    })),
                    onChange: (v) => handleInputChange('duration', v),
                  },
                ]
              : []),
            ...(needsDualAudio
              ? [
                  {
                    key: 'language',
                    label: '演唱语言',
                    value: inputs.language,
                    options: MUSIC_SVS_LANGUAGES,
                    onChange: (v) => handleInputChange('language', v),
                  },
                  {
                    key: 'control',
                    label: '控制方式',
                    value: inputs.control,
                    options: MUSIC_SVS_CONTROLS,
                    onChange: (v) => handleInputChange('control', v),
                  },
                ]
              : []),
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
            needsDualAudio
              ? '上传音色参考与目标曲/伴奏即可开始合成'
              : needsAudio
                ? `上传${mode === 'cover' ? '参考' : '源'}音频，再描述想要的改动`
                : isT2M
                  ? '描述想要的音乐风格，可选填歌词'
                  : '描述想要的音效，如「雨打在铁皮屋顶上」'
          }
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        disabled={
          turnLimitReached || missingRequiredAudio || missingRequiredVideo
        }
        // 歌声合成不需要文本（发送固定标签占位），别拦住只传了两段音频就想发的用户。
        allowEmpty={!needsText}
        placeholder={
          missingRequiredAudio
            ? '请先上传音频'
            : missingRequiredVideo
              ? '请先上传视频'
              : needsDualAudio
                ? '可留空直接合成，或补充演唱要求…'
                : isT2M
                  ? '描述音乐风格…'
                  : needsAudio
                    ? '描述想要的改动…'
                    : '描述音效…'
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
          <div style={{ flex: 1, minHeight: 0 }}>
            <MusicBody key={mode} mode={mode} />
          </div>
        </>
      )}
    </div>
  );
};

export default Music;
