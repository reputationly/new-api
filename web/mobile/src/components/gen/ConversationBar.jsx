import React, { useState } from 'react';
import { Button, Dialog, Popup } from 'antd-mobile';

// 生成类页面的会话工具条:左「新建会话」,右「历史」入口(弹出会话列表)。
// 桌面端是常驻的 VideoHistoryPanel/ImageHistoryPanel(Semi + tailwind),移动端窄屏放不下,
// 改成底部 Popup;数据契约与桌面端一致(conversations / openHistoryItem / deleteHistoryItem
// / clearHistory 都由各体验区 hook 导出)。

// 各体验区状态枚举取值合集:VIDEO_STATUS(queued/in_progress/completed/failed/canceled)
// 与 IMAGE_GEN_STATUS(pending/success/failed)。合成一张表,避免按页面分别引常量。
const STATUS_TEXT = {
  queued: '排队中',
  pending: '排队中',
  in_progress: '生成中',
  completed: '已完成',
  success: '已完成',
  failed: '失败',
  canceled: '已取消',
};

const STATUS_COLOR = {
  failed: 'var(--adm-color-danger)',
  canceled: 'var(--adm-color-weak)',
};

const formatTime = (iso) => {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n) => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

// 与桌面端 convSummary 同口径:标题取会话标题、兜底首条提示词;状态取最后一条助手消息。
const summarize = (conv) => {
  const assts = (conv.messages || []).filter((m) => m.role === 'assistant');
  const last = assts[assts.length - 1];
  return {
    title: conv.title || assts[0]?.prompt || '（无提示词）',
    status: last?.status,
    time: conv.updatedAt || conv.createdAt,
  };
};

const ConversationBar = ({
  conversations = [],
  currentConvId,
  showNew,
  onNew,
  onOpen,
  onDelete,
  onClear,
}) => {
  const [visible, setVisible] = useState(false);

  const handleOpen = (conv) => {
    onOpen(conv);
    setVisible(false);
  };

  const handleDelete = async (id) => {
    const result = await Dialog.confirm({ content: '删除这条会话记录？' });
    if (result) onDelete(id);
  };

  const handleClear = async () => {
    const result = await Dialog.confirm({ content: '清空全部历史会话？' });
    if (result) {
      onClear();
      setVisible(false);
    }
  };

  return (
    <>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 8,
          padding: '4px 0',
          borderBottom: '0.5px solid rgba(17,24,39,0.06)',
        }}
      >
        {showNew && (
          <Button size='mini' fill='none' onClick={onNew}>
            新建会话
          </Button>
        )}
        <Button size='mini' fill='none' onClick={() => setVisible(true)}>
          历史{conversations.length > 0 ? ` (${conversations.length})` : ''}
        </Button>
      </div>

      <Popup
        visible={visible}
        onMaskClick={() => setVisible(false)}
        onClose={() => setVisible(false)}
        bodyStyle={{
          height: '70vh',
          display: 'flex',
          flexDirection: 'column',
          borderTopLeftRadius: 12,
          borderTopRightRadius: 12,
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '12px 16px',
            borderBottom: '0.5px solid rgba(17,24,39,0.06)',
          }}
        >
          <span style={{ fontSize: 16, fontWeight: 500 }}>对话历史</span>
          {conversations.length > 0 && (
            <Button size='mini' fill='none' color='danger' onClick={handleClear}>
              清空
            </Button>
          )}
        </div>

        <div style={{ flex: 1, overflowY: 'auto' }}>
          {conversations.length === 0 ? (
            <div
              style={{
                padding: 48,
                textAlign: 'center',
                color: 'var(--adm-color-weak)',
                fontSize: 14,
              }}
            >
              暂无历史会话
            </div>
          ) : (
            conversations.map((conv) => {
              const s = summarize(conv);
              const active = conv.id === currentConvId;
              return (
                <div
                  key={conv.id}
                  onClick={() => handleOpen(conv)}
                  style={{
                    padding: '12px 16px',
                    borderBottom: '0.5px solid rgba(17,24,39,0.06)',
                    background: active ? 'var(--adm-color-box)' : undefined,
                  }}
                >
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      gap: 8,
                    }}
                  >
                    <span
                      style={{
                        fontSize: 14,
                        fontWeight: 500,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {conv.model || '未知模型'}
                    </span>
                    <Button
                      size='mini'
                      fill='none'
                      color='danger'
                      onClick={(e) => {
                        e.stopPropagation();
                        handleDelete(conv.id);
                      }}
                    >
                      删除
                    </Button>
                  </div>
                  <div
                    style={{
                      fontSize: 13,
                      color: 'var(--adm-color-weak)',
                      marginTop: 2,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {s.title}
                  </div>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      marginTop: 6,
                      fontSize: 12,
                      color: 'var(--adm-color-weak)',
                    }}
                  >
                    <span style={{ color: STATUS_COLOR[s.status] }}>
                      {STATUS_TEXT[s.status] || ''}
                    </span>
                    <span>{formatTime(s.time)}</span>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </Popup>
    </>
  );
};

export default ConversationBar;
