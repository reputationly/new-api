import React from 'react';
import { Tabs, TabPane } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useAudioGeneration } from '../../hooks/audioPlayground/useAudioGeneration';
import AudioConfigPanel from '../../components/audioPlayground/AudioConfigPanel';
import AudioChatArea from '../../components/audioPlayground/AudioChatArea';
import VideoHistoryPanel from '../../components/videoPlayground/VideoHistoryPanel';

// 语音合成体验区:三栏结构镜像视频页(配置 | 对话 | 历史)。
// 历史面板与视频完全同构(状态常量取值一致),直接复用 VideoHistoryPanel。
const AudioPlaygroundBody = () => {
  const isMobile = useIsMobile();
  const styleState = { isMobile };
  const {
    inputs,
    handleInputChange,
    groups,
    models,
    messages,
    conversations,
    generating,
    locked,
    turnLimitReached,
    missingRequiredVoice,
    refAudioMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  } = useAudioGeneration();

  return (
    <div
      className='flex-1 min-h-0 flex gap-3 mt-1'
      style={{ flexDirection: isMobile ? 'column' : 'row' }}
    >
      <div style={{ width: isMobile ? '100%' : 300, flexShrink: 0 }}>
        <AudioConfigPanel
          inputs={inputs}
          groups={groups}
          models={models}
          onInputChange={handleInputChange}
          disabled={locked}
          refAudioMaxMB={refAudioMaxMB}
          styleState={styleState}
        />
      </div>

      <div className='flex-1 min-w-0'>
        <AudioChatArea
          messages={messages}
          generating={generating}
          turnLimitReached={turnLimitReached}
          missingRequiredVoice={missingRequiredVoice}
          styleState={styleState}
          onSend={generate}
          onRegenerate={regenerate}
          onRefetch={refetch}
          onClear={newConversation}
        />
      </div>

      <div style={{ width: isMobile ? '100%' : 320, flexShrink: 0 }}>
        <VideoHistoryPanel
          history={conversations}
          onNewConversation={newConversation}
          onClear={clearHistory}
          onDelete={deleteHistoryItem}
          onOpen={openHistoryItem}
          styleState={styleState}
        />
      </div>
    </div>
  );
};

const AudioModel = () => {
  const { t } = useTranslation();

  return (
    <div className='h-full'>
      <div className='mt-[60px] h-[calc(100vh-66px)] flex flex-col px-3 pb-2'>
        <Tabs type='line' activeKey='tts' className='flex-shrink-0'>
          <TabPane tab={t('语音合成')} itemKey='tts' />
        </Tabs>

        <AudioPlaygroundBody />
      </div>
    </div>
  );
};

export default AudioModel;
