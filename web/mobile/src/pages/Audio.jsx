import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, CapsuleTabs, Empty, NavBar, TextArea } from 'antd-mobile';

import { useAudioGeneration } from '@classic/hooks/audioPlayground/useAudioGeneration';
import {
  AUDIO_LANGUAGES,
  AUDIO_SPEAKER_PRESETS,
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
import ConfigCollapse from '../components/gen/ConfigCollapse';
import MediaBar from '../components/gen/MediaBar';
import VoiceRecorder from '../components/gen/VoiceRecorder';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import ShareBar from '../components/gen/ShareBar';
import { showError } from '../shims/classic-utils';
import { fileToDataUrl } from '../utils/file';
import { VideoBody } from './Video';

const AudioBody = ({ mode }) => {
  const {
    inputs,
    handleInputChange,
    groups,
    models,
    messages,
    generating,
    locked,
    turnLimitReached,
    missingRequiredVoice,
    needsVoice,
    needsDualRef,
    needsSpeaker,
    needsLanguage,
    needsInstructions,
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
  } = useAudioGeneration(mode);

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  const [recorderVisible, setRecorderVisible] = useState(false);
  const voiceFileRef = useRef(null);
  const usingOwnVoice = inputs.voicePreset === VOICE_UPLOAD_VALUE;
  // 锁定态（选中了某条会话）参数与音色都改不动，见 useAudioGeneration 的 handleInputChange。
  const editDisabled = generating || locked;

  // 录制/上传即视为"要用自己的声音"：把音色切到自定义，省得用户还得回选择器里找
  // 「我的声音」（它排在 10 个预设音色之后，滚不到就等于不存在）。
  const applyOwnVoice = (dataUrl, name) => {
    handleInputChange('voiceData', dataUrl);
    handleInputChange('voiceName', name);
    handleInputChange('voicePreset', VOICE_UPLOAD_VALUE);
  };

  const handlePickVoice = async (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    if (file.size > VOICE_UPLOAD_MAX_MB * 1024 * 1024) {
      showError(`参考音频不能超过 ${VOICE_UPLOAD_MAX_MB}MB`);
      return;
    }
    try {
      applyOwnVoice(await fileToDataUrl(file), file.name);
    } catch (err) {
      showError('读取音频文件失败');
    }
  };

  // 情感合成的参考音色条常驻显示（不再藏进「音色」选择器的最后一项）。
  const voiceSlot = needsVoice && {
    type: 'custom',
    key: 'ownVoice',
    label: '参考音色',
    render: (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {/* 锁定态改不动音色，只留下方的播放器供试听 */}
          {!locked && (
            <>
              <Button
                size='mini'
                fill='outline'
                disabled={editDisabled}
                onClick={() => setRecorderVisible(true)}
              >
                🎤 录制我的声音
              </Button>
              <Button
                size='mini'
                fill='outline'
                disabled={editDisabled}
                onClick={() => voiceFileRef.current?.click()}
              >
                上传音频
              </Button>
            </>
          )}
          <span className='m-media-slot-name'>
            {usingOwnVoice
              ? inputs.voiceData
                ? inputs.voiceName || '已就绪'
                : '待录制/上传'
              : '当前用预设音色'}
          </span>
        </div>
        {usingOwnVoice && inputs.voiceData && (
          <audio
            src={inputs.voiceData}
            controls
            preload='none'
            style={{ width: '100%', marginTop: 6 }}
          />
        )}
        <input
          ref={voiceFileRef}
          type='file'
          accept='audio/*'
          hidden
          onChange={handlePickVoice}
        />
      </div>
    ),
  };

  const mediaSlots = [
    voiceSlot,
    needsDualRef && {
      type: 'single',
      key: 'refAudioData',
      kind: 'audio',
      label: '说话人1 参考音',
      required: true,
      maxMB: refAudioMaxMB,
      value: inputs.refAudioData,
      name: inputs.refAudioName,
      onChange: (v, name) => {
        handleInputChange('refAudioData', v || '');
        handleInputChange('refAudioName', v ? name : '');
      },
    },
    needsDualRef && {
      type: 'single',
      key: 'refAudio2Data',
      kind: 'audio',
      label: '说话人2 参考音',
      required: true,
      maxMB: refAudioMaxMB,
      value: inputs.refAudio2Data,
      name: inputs.refAudio2Name,
      onChange: (v, name) => {
        handleInputChange('refAudio2Data', v || '');
        handleInputChange('refAudio2Name', v ? name : '');
      },
    },
  ];

  const renderAssistant = (m) => (
    <AsyncTaskBubble
      m={m}
      doneStatus='completed'
      resultUrl={m.audioUrl}
      renderResult={(url) => (
        <div>
          <audio controls src={url} style={{ width: '100%' }} />
          <ShareBar
            url={url}
            filename={`tts-${m.taskId || m.id}.mp3`}
            taskId={m.taskId}
          />
        </div>
      )}
      onRetry={() => regenerate(m.prompt)}
      onRefetch={() => refetch(m.id, m.taskId)}
    />
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
            ...(needsVoice
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
            ...(needsSpeaker
              ? [
                  {
                    key: 'speaker',
                    label: '音色',
                    value: inputs.speaker,
                    options: AUDIO_SPEAKER_PRESETS,
                    onChange: (v) => handleInputChange('speaker', v),
                  },
                ]
              : []),
            ...(needsLanguage
              ? [
                  {
                    key: 'language',
                    label: '口音',
                    value: inputs.language,
                    options: AUDIO_LANGUAGES,
                    onChange: (v) => handleInputChange('language', v),
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
      {needsVoice && (
        <VoiceRecorder
          visible={recorderVisible}
          onClose={() => setRecorderVisible(false)}
          onConfirm={(dataUrl) => applyOwnVoice(dataUrl, '录制音频.wav')}
        />
      )}
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
            needsVoice
              ? '选择音色与情感，输入要朗读的文本'
              : needsDualRef
                ? '上传两位说话人的参考音，输入含 [S1]/[S2] 标记的对话脚本'
                : needsInstructions
                  ? '先描述音色特征，再输入要朗读的文本'
                  : '选择音色与口音，输入要朗读的文本'
          }
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        disabled={turnLimitReached || missingRequiredVoice}
        placeholder={
          needsDualRef ? '输入对话脚本，如 [S1]…[S2]…' : '输入要合成的文本…'
        }
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
  const modes = useVisibleModes('audio');
  const [mode, setMode] = useState(modes[0]?.key || 'emotion');
  useEffect(() => {
    if (modes.length && !modes.some((m) => m.key === mode))
      setMode(modes[0].key);
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
            {/* 视频配音：入口在语音页，但输入是视频、产物也是视频，复用视频体验区
                （与桌面端同一分流，走 useVideoGeneration 的 task_type=v2a）。 */}
            {mode === 'dub' ? (
              <VideoBody key={mode} mode='dub' />
            ) : (
              <AudioBody key={mode} mode={mode} />
            )}
          </div>
        </>
      )}
    </div>
  );
};

export default Audio;
