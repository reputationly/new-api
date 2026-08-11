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
import { tabHasField } from '@classic/constants/playgroundAdmin.constants';
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
export const VideoBody = ({ mode, category = 'video' }) => {
  const {
    isI2V,
    isR2VA,
    isFLF2V,
    isS2V,
    isSR,
    isVACE,
    isDub,
    pipelineModel,
    needsImage,
    isFlf2vSelected,
    allowLastFrame,
    isKeyframeAuto,
    isKeyframeAutoFull,
    maxRefImages,
    maxRefVideos,
    refVideoMaxMB,
    refVideoMaxSec,
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
  } = useVideoGeneration({ mode, category, allowDub: false });

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  // 图片沿用后台配额（0=不限）；视频再压一道移动端闸门，见 MOBILE_MAX_VIDEO_MB。
  const imageMaxMB = maxInputMB;
  const videoMaxMB = maxInputMB
    ? Math.min(maxInputMB, MOBILE_MAX_VIDEO_MB)
    : MOBILE_MAX_VIDEO_MB;
  // 关键帧三态（判据见 keyframeModeOf，由 useVideoGeneration 统一算好：要读运营配置里的
  // taskType 声明，不能只看名字）：
  //   flf2v 尾帧必填 | i2v 不认尾帧（两个引擎实例，task 启动期定死）
  //   auto  两槽都可选、至少填一个 —— MiniMax H3 这类一个 checkpoint 同时吃
  //         首帧/尾帧/首尾帧的模型，靠 frame_indices 区分
  // 用 allowLastFrame 而不是 isFlf2vSelected：后者在 auto 下为 false，会让手机端
  // 拿不到「只给尾帧」(l2va) 与首尾帧两种玩法。校验与派生都在共享 hook 里，这里只管渲染。
  const needsLastFrame = isFLF2V && allowLastFrame;
  const needsVideoUpload = isVACE || isSR || isDub;
  // 参考视频这一模态是否开放（运营配的个数 >0）。开放之后参考图就不再是必填——
  // 视觉参考「图或视频至少其一」，判据在共享 hook 的 missingRequiredImage 里。
  const refVideosOpen = isR2VA && maxRefVideos > 0;
  // 锁定态（选中了某条会话）参数与素材都改不动，见 useVideoGeneration 的 handleInputChange。
  const editDisabled = generating || locked;
  // 哪些参数控件出现在本 tab，统一读中央元数据的 fields（playgroundAdmin.constants.js），
  // 不再在这里按 isSR/isDub/isS2V/followsInput 重写一遍——桌面端 VideoConfigPanel、
  // admin 页各有一份判断时，加一个玩法要改三处且极易漏。
  // 语义仍与原来一致：输出时长由输入决定的玩法（配音跟源视频、数字人跟驱动音频）不声明
  // durations，尺寸/比例跟随输入图的玩法不声明 sizes/aspectRatios。
  const showDuration = tabHasField(category, mode, 'durations');
  const showSize = tabHasField(category, mode, 'sizes');
  const showAspectRatio = tabHasField(category, mode, 'aspectRatios');

  const mediaSlots = [
    needsImage && {
      type: 'single',
      key: 'firstFrame',
      kind: 'image',
      label: isS2V ? '人物图' : '首帧',
      // 关键帧 auto 下首帧也是可选的 —— 只给尾帧(l2va)是合法玩法，引擎按
      // frame_indices=[-1] 反推开头。其余玩法首帧仍必填。
      required: !(isFLF2V && isKeyframeAutoFull),
      maxMB: imageMaxMB,
      value: inputs.firstFrame,
      onChange: (v) => handleInputChange('firstFrame', v),
    },
    needsLastFrame && {
      type: 'single',
      key: 'lastFrame',
      kind: 'image',
      label: '尾帧',
      // auto 下首尾两槽都是可选的（至少填一个，由 hook 的 missingRequiredImage 校验）。
      required: !isKeyframeAuto,
      maxMB: imageMaxMB,
      value: inputs.lastFrame,
      onChange: (v) => handleInputChange('lastFrame', v),
    },
    // 参考生视频：参考视频（可选，纯 opt-in）。运营没配个数就整个不出——手机端
    // 的 MediaListSlot 是图片专用的（accept 与查看器都写死 image/*），所以这里用
    // 多个单文件槽拼，而不是塞进 refImages 那种 list 槽。
    // 体积/时长走参考视频自己的上限，不跟参考图共用 imageMaxMB。
    // 锁定态（看历史会话）按会话里**实际存了几个**渲染，不按当前上限：运营事后把
    // 上限调小，不该让老会话里用过的素材从界面上消失——它们仍会随「重新生成」发出去。
    ...(isR2VA && locked
      ? (inputs.refVideos || []).filter(Boolean).map((val, i) => ({
          type: 'single',
          key: `refVideo-view-${i}`,
          kind: 'video',
          label: `参考视频 ${i + 1}`,
          value: val,
          onChange: () => {},
        }))
      : []),
    ...(isR2VA && !locked && maxRefVideos > 0
      ? Array.from({ length: maxRefVideos }, (_, i) => ({
          type: 'single',
          key: `refVideo-${i}`,
          kind: 'video',
          label:
            maxRefVideos > 1 ? `参考视频 ${i + 1}（可选）` : '参考视频（可选）',
          // videoMaxMB 是 Math.min(maxInputMB, MOBILE_MAX_VIDEO_MB) 的结果，
          // 是**天花板不是默认值**：运营为桌面端配的 50MB 直接拿来用，正好绕开
          // 移动端那道闸（整段 base64 塞进请求体，弱网下必挂）。所以要取二者较小。
          maxMB:
            refVideoMaxMB > 0
              ? Math.min(refVideoMaxMB, videoMaxMB)
              : videoMaxMB,
          maxSec: refVideoMaxSec,
          value: (inputs.refVideos || [])[i] || '',
          onChange: (v) => {
            const next = Array.from(
              { length: maxRefVideos },
              (_, j) => (inputs.refVideos || [])[j] || '',
            );
            next[i] = v || '';
            handleInputChange('refVideos', next);
          },
        }))
      : []),
    // 参考生视频：音色参考（可选）。与数字人的「驱动音频」是两回事——它只提供音色/
    // 说话风格，长度与输出时长无关；要说什么写在提示词里。
    isR2VA && {
      type: 'single',
      key: 'audioData',
      kind: 'audio',
      label: '音色参考（可选）',
      maxMB: imageMaxMB,
      value: inputs.audioData,
      onChange: (v) => handleInputChange('audioData', v),
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
    // 第二源视频只在锁定态出现，且只有收口之前存下来的老会话才有值——它们仍会随
    // 续问/重新生成按 mv2v/ads2v 原样发出去，界面不显示等于骗人（同上一段的理由，
    // 详见 classic useVideoGeneration 的 VIDEO_MODES.vace）。新会话恒为空。
    isVACE &&
      locked &&
      inputs.srcVideo2 && {
        type: 'single',
        key: 'srcVideo2',
        kind: 'video',
        label: '第二视频',
        value: inputs.srcVideo2,
        onChange: () => {},
      },
    // 图生视频(Bernini r2v)：参考图必填，定义主体/服装/道具/场景；视频编辑里则是可选。
    // 参考生视频(r2va)：同一组控件，上限由 hook 给到 9（H3 ∩ Seedance 的交集）。
    // **加 tab 时这里必须一起加**：手机端 tab 的 mobile 开关默认是开的
    // （getTabDisplay 的 mobile: v.mobile !== false），漏了不会隐藏、只会得到一个
    // 没有任何上传入口却又被 missingRequiredImage 灰着发送键的死胡同。
    // maxRefImages 只管可编辑态；锁定态照旧展示会话里存的那些（理由同参考视频）。
    (isI2V || isR2VA || isVACE) &&
      (locked || maxRefImages > 0) && {
        type: 'list',
        key: 'refImages',
        label: isVACE || refVideosOpen ? '参考图（可选）' : '参考图',
        required: isI2V || (isR2VA && !refVideosOpen),
        max: maxRefImages,
        maxMB: imageMaxMB,
        values: inputs.refImages || [],
        onChange: (v) => handleInputChange('refImages', v),
      },
  ];

  const missingHint =
    isI2V || isR2VA
      ? refVideosOpen
        ? '请先上传参考图或参考视频'
        : '请先上传参考图'
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
        : isI2V || isR2VA
          ? refVideosOpen
            ? '上传参考图或参考视频，并输入提示词开始生成'
            : '上传参考图并输入提示词开始生成'
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
    // 已完成却没有结果地址(本地缓存丢失且无 taskId 可重建,或后端已清理):如实说失效,
    // 别落到下面的进度分支被显示成永远的「生成中」。
    if (m.status === 'completed') {
      return (
        <div>
          <div style={{ color: 'var(--adm-color-weak)' }}>
            结果已失效，请重新生成
          </div>
          <Button
            size='mini'
            fill='outline'
            style={{ marginTop: 8 }}
            onClick={() => regenerate(m.prompt)}
          >
            重新生成
          </Button>
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
    // queued / in_progress。进度封顶 99%:上游偶尔在终态落地前先报 100,
    // 「100% 还在生成中」看起来就是卡死了,100% 只留给真正完成的那一刻。
    const percent =
      typeof m.progress === 'number' ? Math.min(99, Math.round(m.progress)) : 0;
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        {percent > 0 ? (
          <ProgressCircle percent={percent}>{percent}%</ProgressCircle>
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
    showSize && inputs.size,
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
            ...(showSize
              ? [
                  {
                    key: 'size',
                    label: '尺寸',
                    value: inputs.size,
                    options: availableSizes,
                    onChange: (v) => handleInputChange('size', v),
                  },
                ]
              : []),
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
            ...(showAspectRatio
              ? [
                  {
                    key: 'aspectRatio',
                    label: '比例',
                    value: inputs.aspectRatio,
                    options: availableAspectRatios,
                    onChange: (v) => handleInputChange('aspectRatio', v),
                  },
                ]
              : []),
          ]}
        >
          {/* 插帧开关：默认关，透传 target_fps。target_fps 是自建引擎(gpustackplus)的
              RIFE 字段，第三方渠道不认，故只对自建流水线模型渲染。超分/配音本身就是
              后处理，不适用，也不渲染。
              只写「插帧：关」不带说明：一句解释就撑满一行，而这个词本身够自解释。
              配音开关手机端不提供，见上面的 allowDub。 */}
          {pipelineModel && !isSR && !isDub && (
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
        {pipelineModel && showSize && /1080/i.test(inputs.size || '') && (
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
        optimizeCategory={category}
        optimizeTab={mode}
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
