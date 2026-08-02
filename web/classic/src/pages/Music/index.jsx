import React, { useCallback, useEffect, useState } from 'react';
import { Tabs, TabPane } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { showError } from '../../helpers';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { usePlaygroundTabs } from '../../hooks/common/usePlaygroundTabs';
import { useMusicGeneration } from '../../hooks/musicPlayground/useMusicGeneration';
import MusicConfigPanel from '../../components/musicPlayground/MusicConfigPanel';
import MusicChatArea from '../../components/musicPlayground/MusicChatArea';
import VideoHistoryPanel from '../../components/videoPlayground/VideoHistoryPanel';

// 单个玩法的三栏体验区。切 tab 时整体重挂载,各玩法历史/参数互不串扰(mode 作为 key)。
// 涵盖 ACE-Step(文生音乐/音乐改编/音乐重绘)与 AudioX/SoulX(文生音效/视频配音效/视频
// 配乐/歌声合成)。历史面板与视频/语音同构,直接复用。
const MusicPlaygroundBody = ({ mode, initialSrcTaskId = '', onSendToSrc }) => {
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
    showAssistModel,
    drafting,
    draftPlan,
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

  // 从「改编风格 / 重绘片段」跳进来:把上一首的产物设为源音频。发的是 task:<task_id>,
  // 后端在共享盘上直读(nfsinput/taskref.go),不必让用户下载再上传。切 tab 会整体重挂载,
  // 所以交接值由父组件透传,在这里落到 inputs。
  useEffect(() => {
    if (!initialSrcTaskId) return;
    handleInputChange('srcTaskId', initialSrcTaskId);
    handleInputChange('srcTaskLabel', t('上一首生成结果'));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialSrcTaskId]);

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
          mode={mode}
          engine={engine}
          needsAudio={needsAudio}
          needsVideo={needsVideo}
          needsDualAudio={needsDualAudio}
          showTranslation={showTranslation}
          showAssistModel={showAssistModel}
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
          drafting={drafting}
          onDraftPlan={
            showAssistModel && !showTranslation ? draftPlan : undefined
          }
          onSendToSrc={onSendToSrc}
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

const MusicModel = () => {
  const { t } = useTranslation();
  const tabs = usePlaygroundTabs('music');
  const [activeTab, setActiveTab] = useState(tabs[0]?.key || 't2m');
  // 「改编风格 / 重绘片段」的跨 tab 交接:{mode, taskId}。切 tab 会重挂载 body,
  // hook state 不跨 tab,故由这里暂存一次、透传给目标 tab。
  const [pendingSrc, setPendingSrc] = useState(null);

  const sendToSrc = useCallback(
    (targetMode, taskId) => {
      if (!taskId) return;
      // 目标玩法可能被「体验区管理」关掉了,那就不该跳过去。
      if (!tabs.some((tb) => tb.key === targetMode)) {
        showError(t('该玩法当前未开放'));
        return;
      }
      setPendingSrc({ mode: targetMode, taskId });
      setActiveTab(targetMode);
    },
    [tabs, t],
  );

  useEffect(() => {
    if (tabs.length && !tabs.some((tb) => tb.key === activeTab)) {
      setActiveTab(tabs[0].key);
    }
  }, [tabs, activeTab]);

  if (!tabs.length) return null;

  return (
    <div className='h-full'>
      <div className='mt-[60px] h-[calc(100vh-66px)] flex flex-col px-3 pb-2'>
        <Tabs
          type='line'
          activeKey={activeTab}
          onChange={setActiveTab}
          className='flex-shrink-0'
        >
          {tabs.map((tb) => (
            <TabPane key={tb.key} tab={t(tb.label)} itemKey={tb.key} />
          ))}
        </Tabs>

        <MusicPlaygroundBody
          key={activeTab}
          mode={activeTab}
          initialSrcTaskId={
            pendingSrc?.mode === activeTab ? pendingSrc.taskId : ''
          }
          onSendToSrc={sendToSrc}
        />
      </div>
    </div>
  );
};

export default MusicModel;
