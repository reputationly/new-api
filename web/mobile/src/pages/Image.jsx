import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  CapsuleTabs,
  Empty,
  Image as AmImage,
  ImageViewer,
  NavBar,
  SpinLoading,
} from 'antd-mobile';

import { useImageGeneration } from '@classic/hooks/imagePlayground/useImageGeneration';
import { IMAGE_MAX_EDIT_IMAGES } from '@classic/constants/imagePlayground.constants';

import { useVisibleModes } from '../hooks/useVisibleModes';
import { useAutoOpenLatest } from '../hooks/useAutoOpenLatest';
import ConfigBar from '../components/gen/ConfigBar';
import ConfigCollapse from '../components/gen/ConfigCollapse';
import ConversationBar from '../components/gen/ConversationBar';
import MediaBar from '../components/gen/MediaBar';
import MessageFeed from '../components/gen/MessageFeed';
import PromptBar from '../components/gen/PromptBar';
import ShareBar from '../components/gen/ShareBar';

const ImageBody = ({ mode }) => {
  const {
    isI2I,
    inputs,
    handleInputChange,
    groups,
    models,
    availableSizes,
    messages,
    generating,
    locked,
    turnLimitReached,
    missingRequiredImage,
    generate,
    regenerate,
    newConversation,
    conversations,
    currentConvId,
    openHistoryItem,
    deleteHistoryItem,
    clearHistory,
  } = useImageGeneration({ mode });

  useAutoOpenLatest(conversations, currentConvId, openHistoryItem);

  const [viewerImage, setViewerImage] = useState('');

  const renderAssistant = (m) => {
    if (m.status === 'success' && (m.images || []).length > 0) {
      return (
        <div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {m.images.map((url, i) => (
              <AmImage
                key={i}
                src={url}
                width={140}
                height={140}
                fit='cover'
                style={{ borderRadius: 8 }}
                onClick={() => setViewerImage(url)}
              />
            ))}
          </div>
          <ShareBar
            url={m.images[0]}
            filename={`image-${m.id}.png`}
            hint='微信内可直接长按图片转发好友或保存到相册。'
          />
        </div>
      );
    }
    // 已成功但图没了(base64 图不落盘/本地缓存被清理):同桌面端如实说过期,别落到下面的
    // 进度分支被显示成永远的「生成中」。
    if (m.status === 'success') {
      return (
        <div>
          <div style={{ color: 'var(--adm-color-weak)' }}>
            图片已过期或本地缓存被清理，请重新生成
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
    if (m.status === 'failed') {
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
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <SpinLoading style={{ '--size': '24px' }} />
        <span>生成中…</span>
      </div>
    );
  };

  // 锁定态（选中了某条会话）参数与底图都改不动，见 useImageGeneration 的 handleInputChange。
  const editDisabled = generating || locked;
  const mediaSlots = [
    isI2I && {
      type: 'list',
      key: 'imageUrls',
      label: '底图',
      required: true,
      max: IMAGE_MAX_EDIT_IMAGES,
      values: inputs.imageUrls || [],
      onChange: (v) => handleInputChange('imageUrls', v),
    },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <ConfigCollapse
        locked={locked}
        title={[inputs.model, inputs.size].filter(Boolean).join(' · ')}
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
            {
              key: 'size',
              label: '尺寸',
              value: inputs.size,
              options: availableSizes,
              onChange: (v) => handleInputChange('size', v),
            },
          ]}
        />
        <MediaBar
          disabled={editDisabled}
          readOnly={locked}
          slots={mediaSlots}
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
            isI2I ? '上传底图并输入提示词开始编辑' : '输入提示词开始生成图片'
          }
        />
      </div>
      <PromptBar
        onSend={generate}
        generating={generating}
        disabled={turnLimitReached || missingRequiredImage}
        placeholder={
          turnLimitReached
            ? '本会话轮数已达上限，请新建会话'
            : missingRequiredImage
              ? '请先上传底图'
              : '描述你想要的图片…'
        }
        // 图片生成是一次同步请求,没有 taskId 可续查:切标签/返回都会卸载本页,在途请求
        // 随之作废(下次进来只会看到「生成已中断」,见 useImageGeneration 的
        // markInterruptedAsFailed)。视频/音乐那套「切走再回来接着轮询」在这里不成立,
        // 只能把话说在前面 —— 仅生成中显示,平时不占位。
        extra={
          generating ? (
            <div className='m-locked-hint' style={{ padding: '0 4px 8px' }}>
              生成中，请勿切换标签或返回上一页，否则本次生成会中断。
            </div>
          ) : null
        }
      />
      <ImageViewer
        image={viewerImage}
        visible={!!viewerImage}
        onClose={() => setViewerImage('')}
      />
    </div>
  );
};

const ImagePage = () => {
  const navigate = useNavigate();
  const modes = useVisibleModes('image');
  const [mode, setMode] = useState(modes[0]?.key || 'text2image');
  useEffect(() => {
    if (modes.length && !modes.some((m) => m.key === mode))
      setMode(modes[0].key);
  }, [modes, mode]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <NavBar onBack={() => navigate(-1)}>图片生成</NavBar>
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
            <ImageBody key={mode} mode={mode} />
          </div>
        </>
      )}
    </div>
  );
};

export default ImagePage;
