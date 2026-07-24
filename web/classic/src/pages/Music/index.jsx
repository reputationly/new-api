import React, { useState } from 'react';
import { Tabs, TabPane } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useMusicGeneration } from '../../hooks/musicPlayground/useMusicGeneration';
import MusicConfigPanel from '../../components/musicPlayground/MusicConfigPanel';
import MusicChatArea from '../../components/musicPlayground/MusicChatArea';
import VideoHistoryPanel from '../../components/videoPlayground/VideoHistoryPanel';
import { MUSIC_TAB_ORDER } from '../../constants/musicPlayground.constants';

// 单个玩法的三栏体验区。切 tab 时整体重挂载,各玩法历史/参数互不串扰(mode 作为 key)。
// 涵盖 ACE-Step(文生音乐/音乐改编/音乐重绘)与 AudioX/SoulX(文生音效/视频配音效/视频
// 配乐/歌声合成)。历史面板与视频/语音同构,直接复用。
const MusicPlaygroundBody = ({ mode }) => {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const styleState = { isMobile };
  const {
    inputs,
    handleInputChange,
    applyExample,
    groups,
    models,
    messages,
    conversations,
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
    showTranslation,
    translationGroups,
    translationModels,
    refAudioMaxMB,
    videoMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  } = useMusicGeneration(mode);

  // cover=参考音频 / repaint=源音频(其余玩法无单音频标签)。
  const audioLabel =
    mode === 'cover' ? t('参考音频') : mode === 'repaint' ? t('源音频') : '';

  // 各玩法欢迎语。
  const welcomeText =
    mode === 't2a'
      ? t('欢迎使用 AI 文生音效,请在左侧选择模型,并在下方输入音效描述')
      : mode === 'svs'
        ? t('欢迎使用 AI 歌声合成,请在左侧上传音色参考与目标曲/伴奏')
        : '';

  return (
    <div
      className='flex-1 min-h-0 flex gap-3 mt-1'
      style={{ flexDirection: isMobile ? 'column' : 'row' }}
    >
      <div style={{ width: isMobile ? '100%' : 300, flexShrink: 0 }}>
        <MusicConfigPanel
          inputs={inputs}
          groups={groups}
          models={models}
          onInputChange={handleInputChange}
          disabled={locked}
          engine={engine}
          needsAudio={needsAudio}
          needsVideo={needsVideo}
          needsDualAudio={needsDualAudio}
          showTranslation={showTranslation}
          translationGroups={translationGroups}
          translationModels={translationModels}
          audioLabel={audioLabel}
          refAudioMaxMB={refAudioMaxMB}
          videoMaxMB={videoMaxMB}
          styleState={styleState}
        />
      </div>

      <div className='flex-1 min-w-0'>
        <MusicChatArea
          messages={messages}
          generating={generating}
          turnLimitReached={turnLimitReached}
          missingRequiredAudio={missingRequiredAudio}
          missingRequiredVideo={missingRequiredVideo}
          engine={engine}
          mode={mode}
          needsText={needsText}
          needsVideo={needsVideo}
          needsDualAudio={needsDualAudio}
          showTranslation={showTranslation}
          welcomeText={welcomeText}
          onApplyExample={applyExample}
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

// 5 个子标签页(标签文案 = 能力中文)。
// 「视频生音」(v2a)已于 2026-07 下线:视频配乐移交 LTX-2.3,入口在语音模型页。
const TAB_LABELS = {
  t2m: '文生音乐',
  cover: '音乐改编',
  repaint: '音乐重绘',
  t2a: '文生音效',
  svs: '歌声合成',
};

const MusicModel = () => {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState('t2m');

  return (
    <div className='h-full'>
      <div className='mt-[60px] h-[calc(100vh-66px)] flex flex-col px-3 pb-2'>
        <Tabs
          type='line'
          activeKey={activeTab}
          onChange={setActiveTab}
          className='flex-shrink-0'
        >
          {MUSIC_TAB_ORDER.map((mode) => (
            <TabPane key={mode} tab={t(TAB_LABELS[mode])} itemKey={mode} />
          ))}
        </Tabs>

        <MusicPlaygroundBody key={activeTab} mode={activeTab} />
      </div>
    </div>
  );
};

export default MusicModel;
