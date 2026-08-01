import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, CapsuleTabs, Empty, NavBar, TextArea } from 'antd-mobile';

import { useAudioGeneration } from '@classic/hooks/audioPlayground/useAudioGeneration';
import {
  EMOTION_PRESETS,
  PRESET_VOICES,
  VOICE_UPLOAD_MAX_MB,
  VOICE_UPLOAD_VALUE,
} from '@classic/constants/audioPlayground.constants';

import AsyncTaskBubble from '../components/gen/AsyncTaskBubble';
import { useVisibleModes } from '../hooks/useVisibleModes';
import { useAutoOpenLatest } from '../hooks/useAutoOpenLatest';
import ConfigBar from '../components/gen/ConfigBar';
import ConversationBar from '../components/gen/ConversationBar';
import VoiceRecorder from '../components/gen/VoiceRecorder';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import { showError } from '../shims/classic-utils';
import { fileToDataUrl } from '../utils/file';
import ShareBar from '../components/gen/ShareBar';

// 一期移动端开放不需要上传参考音的两个模式：情感合成（预置音色）与声音设计。
// 克隆/双人对话需上传参考音频，引导桌面端。
const MODES = [
  { key: 'emotion', title: '情感合成' },
  { key: 'design', title: '声音设计' },
];

const AudioBody = ({ mode }) => {
  const {
    inputs,
    handleInputChange,
    groups,
    models,
    messages,
    generating,
    turnLimitReached,
    missingRequiredVoice,
    needsInstructions,
    generate,
    regenerate,
    refetch,
    newConversation,
    conversations,
    currentConvId,
    openHistoryItem,
    deleteHistoryItem,
    clearHistory,
  } = useAudioGeneration(mode);

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  const isEmotion = mode === 'emotion';
  const [recorderVisible, setRecorderVisible] = useState(false);
  const voiceFileRef = useRef(null);

  const handlePickVoice = async (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (file.size > VOICE_UPLOAD_MAX_MB * 1024 * 1024) {
      showError(`参考音频不能超过 ${VOICE_UPLOAD_MAX_MB}MB`);
      return;
    }
    try {
      const dataUrl = await fileToDataUrl(file);
      handleInputChange('voiceData', dataUrl);
      handleInputChange('voiceName', file.name);
    } catch (err) {
      showError('读取音频文件失败');
    }
  };

  const renderAssistant = (m) => (
    <AsyncTaskBubble
      m={m}
      doneStatus='completed'
      resultUrl={m.audioUrl}
      renderResult={(url) => (
        <div>
          <audio controls src={url} style={{ width: '100%' }} />
          <ShareBar url={url} filename={`tts-${m.taskId || m.id}.mp3`} />
        </div>
      )}
      onRetry={() => regenerate(m.prompt)}
      onRefetch={() => refetch(m.id, m.taskId)}
    />
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
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
          ...(isEmotion
            ? [
                {
                  key: 'voicePreset',
                  label: '音色',
                  value: inputs.voicePreset,
                  options: [
                    ...PRESET_VOICES.map((v) => ({
                      label: v.label,
                      value: v.id,
                    })),
                    { label: '我的声音', value: VOICE_UPLOAD_VALUE },
                  ],
                  onChange: (v) => handleInputChange('voicePreset', v),
                },
                {
                  key: 'emotion',
                  label: '情感',
                  value: inputs.emotion,
                  options: EMOTION_PRESETS,
                  onChange: (v) => handleInputChange('emotion', v),
                },
              ]
            : []),
        ]}
      />
      {/* 自定义音色(录制/上传):data-url 写进 voiceData,与桌面端「上传自定义音频」同一条链路 */}
      {isEmotion && inputs.voicePreset === VOICE_UPLOAD_VALUE && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '8px 12px',
            borderBottom: '0.5px solid rgba(17,24,39,0.06)',
          }}
        >
          <Button
            size='mini'
            fill='outline'
            disabled={generating}
            onClick={() => setRecorderVisible(true)}
          >
            🎤 录制
          </Button>
          <Button
            size='mini'
            fill='outline'
            disabled={generating}
            onClick={() => voiceFileRef.current?.click()}
          >
            上传
          </Button>
          <span style={{ fontSize: 12, color: 'var(--adm-color-weak)' }}>
            {inputs.voiceData ? inputs.voiceName || '已就绪' : '未设置'}
          </span>
          <input
            ref={voiceFileRef}
            type='file'
            accept='audio/*'
            hidden
            onChange={handlePickVoice}
          />
        </div>
      )}
      {isEmotion &&
        inputs.voicePreset === VOICE_UPLOAD_VALUE &&
        inputs.voiceData && (
          <audio
            src={inputs.voiceData}
            controls
            preload='none'
            style={{ width: '100%', height: 32 }}
          />
        )}
      {isEmotion && (
        <VoiceRecorder
          visible={recorderVisible}
          onClose={() => setRecorderVisible(false)}
          onConfirm={(dataUrl) => {
            handleInputChange('voiceData', dataUrl);
            handleInputChange('voiceName', '录制音频.wav');
          }}
        />
      )}
      <ConversationBar
        conversations={conversations}
        currentConvId={currentConvId}
        showNew={messages.length > 0}
        onNew={newConversation}
        onOpen={openHistoryItem}
        onDelete={deleteHistoryItem}
        onClear={clearHistory}
      />
      <div style={{ flex: 1, overflowY: 'auto' }}>
        <MessageFeed
          messages={messages}
          renderAssistant={renderAssistant}
          empty={
            isEmotion
              ? '选择音色与情感，输入要朗读的文本'
              : '先描述音色特征，再输入要朗读的文本'
          }
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        disabled={turnLimitReached || missingRequiredVoice}
        placeholder='输入要合成的文本…'
        extra={
          needsInstructions ? (
            <div
              style={{
                background: 'var(--adm-color-fill-content, #f5f5f5)',
                borderRadius: 8,
                padding: '6px 10px',
                marginBottom: 8,
              }}
            >
              <TextArea
                placeholder='音色描述（必填），如「低沉沙哑的中年男声，语速缓慢」'
                value={inputs.instructions}
                onChange={(v) => handleInputChange('instructions', v)}
                rows={2}
                autoSize={{ minRows: 2, maxRows: 4 }}
              />
            </div>
          ) : null
        }
      />
    </div>
  );
};

const Audio = () => {
  const navigate = useNavigate();
  const modes = useVisibleModes('audio', MODES);
  const [mode, setMode] = useState(modes[0]?.key || MODES[0].key);
  useEffect(() => {
    if (modes.length && !modes.some((m) => m.key === mode)) setMode(modes[0].key);
  }, [modes, mode]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar onBack={() => navigate(-1)}>语音合成</NavBar>
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
            <AudioBody key={mode} mode={mode} />
          </div>
        </>
      )}
    </div>
  );
};

export default Audio;
