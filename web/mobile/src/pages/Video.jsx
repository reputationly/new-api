import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  CapsuleTabs,
  Empty,
  NavBar,
  ProgressCircle,
  SpinLoading,
} from 'antd-mobile';

import { useVideoGeneration } from '@classic/hooks/videoPlayground/useVideoGeneration';
import { isFlf2vModel } from '@classic/constants/videoPlayground.constants';
import { useVisibleModes, useDesktopOnlyHint } from '../hooks/useVisibleModes';
import { useAutoOpenLatest } from '../hooks/useAutoOpenLatest';

import ConfigBar from '../components/gen/ConfigBar';
import ConfigCollapse from '../components/gen/ConfigCollapse';
import ConversationBar from '../components/gen/ConversationBar';
import MediaBar, {
  MOBILE_MAX_VIDEO_MB,
  MOBILE_RECORD_VIDEO_MAX_SEC,
} from '../components/gen/MediaBar';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import ShareBar from '../components/gen/ShareBar';

// 视频体验区。文生/图生/关键帧/数字人/视频编辑共用本组件，输入形态按 mode 分流；
// 「视频配音」(dub) 的入口挂在语音页，但产物是视频，也复用这里（与桌面端一致）。
export const VideoBody = ({ mode }) => {
  const {
    isI2V,
    isFLF2V,
    isS2V,
    isSR,
    isVACE,
    isDub,
    needsImage,
    followsInput,
    maxRefImages,
    maxInputMB,
    inputs,
    handleInputChange,
    groups,
    models,
    availableSizes,
    availableDurations,
    availableAspectRatios,
    messages,
    generating,
    locked,
    turnLimitReached,
    missingRequiredImage,
    generate,
    regenerate,
    refetch,
    newConversation,
    conversations,
    currentConvId,
    openHistoryItem,
    deleteHistoryItem,
    clearHistory,
    // allowDub:false —— 手机端不做「生成后自动配音」：要多排一次 v2a，等待更久、
    // 失败面更大，统一去桌面端做。它也堵住历史会话里存了 dubbing:true 的续问。
  } = useVideoGeneration({ mode, allowDub: false });

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  // 图片沿用后台配额（0=不限）；视频再压一道移动端闸门，见 MOBILE_MAX_VIDEO_MB。
  const imageMaxMB = maxInputMB;
  const videoMaxMB = maxInputMB
    ? Math.min(maxInputMB, MOBILE_MAX_VIDEO_MB)
    : MOBILE_MAX_VIDEO_MB;
  // 关键帧 tab 下 i2v 与 flf2v 是两个引擎实例，只有后者认尾帧（判据见 isFlf2vModel）。
  const needsLastFrame = isFLF2V && isFlf2vModel(inputs.model);
  const needsVideoUpload = isVACE || isSR || isDub;
  // 锁定态（选中了某条会话）参数与素材都改不动，见 useVideoGeneration 的 handleInputChange。
  const editDisabled = generating || locked;
  // 输出时长由输入决定的玩法不下发 duration，也就不该摆时长选择器：超分/配音跟源视频，
  // 数字人跟驱动音频（引擎不读 target_video_length；上限由后台 maxAudioSec 配置，
  // 后端据此下发 video_duration，见 adaptor.go 的 s2v 分支）。
  const showDuration = !isSR && !isDub && !isS2V;

  const mediaSlots = [
    needsImage && {
      type: 'single',
      key: 'firstFrame',
      kind: 'image',
      label: isS2V ? '人物图' : '首帧',
      required: true,
      maxMB: imageMaxMB,
      value: inputs.firstFrame,
      onChange: (v) => handleInputChange('firstFrame', v),
    },
    needsLastFrame && {
      type: 'single',
      key: 'lastFrame',
      kind: 'image',
      label: '尾帧',
      required: true,
      maxMB: imageMaxMB,
      value: inputs.lastFrame,
      onChange: (v) => handleInputChange('lastFrame', v),
    },
    isS2V && {
      type: 'single',
      key: 'audioData',
      kind: 'audio',
      label: '驱动音频',
      required: true,
      maxMB: imageMaxMB,
      value: inputs.audioData,
      onChange: (v) => handleInputChange('audioData', v),
    },
    (isSR || isDub) && {
      type: 'single',
      key: 'sourceVideo',
      kind: 'video',
      label: isDub ? '待配音视频' : '源视频',
      required: true,
      maxMB: videoMaxMB,
      value: inputs.sourceVideo,
      onChange: (v) => handleInputChange('sourceVideo', v),
    },
    isVACE && {
      type: 'single',
      key: 'srcVideo',
      kind: 'video',
      label: '源视频',
      required: true,
      maxMB: videoMaxMB,
      value: inputs.srcVideo,
      onChange: (v) => handleInputChange('srcVideo', v),
    },
    isVACE && {
      type: 'single',
      key: 'srcVideo2',
      kind: 'video',
      label: '第二视频（可选，双视频=多源编辑）',
      maxMB: videoMaxMB,
      value: inputs.srcVideo2,
      onChange: (v) => handleInputChange('srcVideo2', v),
    },
    // 图生视频(Bernini r2v)：参考图必填，定义主体/服装/道具/场景；视频编辑里则是可选。
    (isI2V || isVACE) && {
      type: 'list',
      key: 'refImages',
      label: isI2V ? '参考图' : '参考图（可选）',
      required: isI2V,
      max: maxRefImages,
      maxMB: imageMaxMB,
      values: inputs.refImages || [],
      onChange: (v) => handleInputChange('refImages', v),
    },
  ];

  const missingHint = isI2V
    ? '请先上传参考图'
    : needsLastFrame && !(inputs.lastFrame || '').trim()
      ? '请先上传尾帧'
      : isS2V && !(inputs.audioData || '').trim()
        ? '请先上传驱动音频'
        : needsVideoUpload
          ? '请先上传视频'
          : '请先上传图片';

  const emptyHint = isDub
    ? '上传视频后描述画面里什么在发声，如「脚步踩过落叶」'
    : isVACE
      ? '上传源视频并描述你想要的改动'
      : isS2V
        ? '上传人物图与驱动音频，再描述画面'
        : isI2V
          ? '上传参考图并输入提示词开始生成'
          : needsImage
            ? '上传帧图并输入提示词开始生成'
            : '输入提示词开始生成视频';

  const renderAssistant = (m) => {
    if (m.status === 'completed' && m.videoUrl) {
      return (
        <div>
          <video
            controls
            playsInline
            src={m.videoUrl}
            style={{ width: '100%', borderRadius: 8 }}
          />
          <ShareBar
            url={m.videoUrl}
            filename={`video-${m.taskId || m.id}.mp4`}
            taskId={m.taskId}
          />
        </div>
      );
    }
    if (m.status === 'failed' || m.status === 'canceled') {
      return (
        <div>
          <div style={{ color: 'var(--adm-color-danger)' }}>
            生成失败{m.error ? `：${m.error}` : ''}
          </div>
          <Button
            size='mini'
            fill='outline'
            style={{ marginTop: 8 }}
            onClick={() => regenerate(m.prompt)}
          >
            重试
          </Button>
        </div>
      );
    }
    // queued / in_progress
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        {typeof m.progress === 'number' && m.progress > 0 ? (
          <ProgressCircle percent={Math.round(m.progress)}>
            {Math.round(m.progress)}%
          </ProgressCircle>
        ) : (
          <SpinLoading style={{ '--size': '24px' }} />
        )}
        <div>
          <div>
            {m.stage === 'upscaling'
              ? '画质增强中（超分）…'
              : m.stage === 'dubbing'
                ? '配音中…'
                : m.status === 'queued'
                  ? '排队中…'
                  : '生成中…'}
          </div>
          {m.pollTimedOut && (
            <Button
              size='mini'
              fill='outline'
              style={{ marginTop: 6 }}
              onClick={() => refetch(m.id, m.taskId)}
            >
              查询结果
            </Button>
          )}
        </div>
      </div>
    );
  };

  const summaryTitle = [
    inputs.model,
    !followsInput && inputs.size,
    showDuration && inputs.seconds && `${inputs.seconds}s`,
  ]
    .filter(Boolean)
    .join(' · ');

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <ConfigCollapse
        locked={locked}
        title={summaryTitle}
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
            // 尺寸/比例只有文生视频会下发；其余玩法输出跟随上传的图/视频，
            // 摆出来就是个点了不生效的假开关（旧版图生视频那个「尺寸：480P」即是）。
            ...(followsInput
              ? []
              : [
                  {
                    key: 'size',
                    label: '尺寸',
                    value: inputs.size,
                    options: availableSizes,
                    onChange: (v) => handleInputChange('size', v),
                  },
                ]),
            ...(showDuration
              ? [
                  {
                    key: 'seconds',
                    label: '时长',
                    value: inputs.seconds,
                    options: availableDurations,
                    onChange: (v) => handleInputChange('seconds', v),
                  },
                ]
              : []),
            ...(followsInput
              ? []
              : [
                  {
                    key: 'aspectRatio',
                    label: '比例',
                    value: inputs.aspectRatio,
                    options: availableAspectRatios,
                    onChange: (v) => handleInputChange('aspectRatio', v),
                  },
                ]),
          ]}
        >
          {/* 插帧开关：默认关，透传 target_fps。超分/配音本身就是后处理，不适用，不渲染。
              只写「插帧：关」不带说明：一句解释就撑满一行，而这个词本身够自解释。
              配音开关手机端不提供，见上面的 allowDub。 */}
          {!isSR && !isDub && (
            <div
              className={`m-config-chip${inputs.interpolation ? ' active' : ''}`}
              onClick={() =>
                !editDisabled &&
                handleInputChange('interpolation', !inputs.interpolation)
              }
            >
              插帧：{inputs.interpolation ? '开' : '关'}
            </div>
          )}
        </ConfigBar>
        {/* 配音不再有独立的提示词框：v2a 段直接复用生成这段视频的提示词 */}
        {!followsInput && /1080/i.test(inputs.size || '') && (
          <div
            style={{
              padding: '6px 12px',
              fontSize: 12,
              color: '#b45309',
              background: '#fffbeb',
              borderBottom: '0.5px solid rgba(17,24,39,0.06)',
            }}
          >
            1080P
            将先生成再调用超分模型提升画质：耗时更久，且会同时产生本模型与超分模型的额度/积分消耗
          </div>
        )}
        <MediaBar
          slots={mediaSlots}
          disabled={editDisabled}
          readOnly={locked}
          notice={
            needsVideoUpload
              ? `视频需整段上传，建议在 WiFi 下操作；单个文件不超过 ${videoMaxMB}MB，现场录像不超过 ${MOBILE_RECORD_VIDEO_MAX_SEC} 秒`
              : ''
          }
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
          empty={emptyHint}
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        disabled={turnLimitReached || missingRequiredImage}
        // 配乐的提示词是可选的（留空=按画面自动配），别拦住只上传视频就想发的用户。
        allowEmpty={isDub}
        placeholder={
          turnLimitReached
            ? '本会话轮数已达上限，请新建会话'
            : missingRequiredImage
              ? missingHint
              : isDub
                ? '描述画面里什么在发声…'
                : '描述你想要的视频…'
        }
      />
    </div>
  );
};

const Video = () => {
  const navigate = useNavigate();
  const modes = useVisibleModes('video');
  const desktopOnlyHint = useDesktopOnlyHint('video');
  const [mode, setMode] = useState(modes[0]?.key || 'text2video');

  useEffect(() => {
    if (modes.length && !modes.some((m) => m.key === mode)) {
      setMode(modes[0].key);
    }
  }, [modes, mode]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar onBack={() => navigate(-1)}>视频生成</NavBar>
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
            <VideoBody key={mode} mode={mode} />
          </div>
        </>
      )}
    </div>
  );
};

export default Video;
