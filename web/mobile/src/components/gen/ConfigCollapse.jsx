import React, { useState } from 'react';
import { Button, Image } from 'antd-mobile';
import { DownOutline, UpOutline } from 'antd-mobile-icons';

// 锁定态下的配置区折叠壳。
//
// 四个体验区的 hook 里 `locked = currentConvId !== null`，而 handleInputChange 开头就是
// `if (lockedRef.current) return`——只要选中了某条会话，参数与素材就已经改不动了，续问
// 走的是会话里存下的那份。移动端又有 useAutoOpenLatest 在挂载时自动选中最近一条，于是
// 进页面即锁定：整屏配置摆在那里，点了却毫无反应。
//
// 这里把锁定态收成一条摘要，并保证「看得见」：缩略图直接摆在摘要上，音视频给角标，
// 点一下展开就是只读的预览与播放器（MediaBar 的 readOnly 分支）。想改参数只能新建会话，
// 按钮就放在摘要条上，不必去下面的会话工具条里找。
//
// 未锁定时本组件完全透明，原样渲染 children。

const KIND_BADGE = { audio: '音频', video: '视频' };
const MAX_THUMBS = 4;

// 摘要素材直接从页面已经拼好的 mediaSlots 推导，避免四个页面各写一遍。
// 约定见 MediaBar：falsy 项跳过，list 用 values、single 用 value。
const summarize = (slots) => {
  const thumbs = [];
  const badges = [];
  slots.filter(Boolean).forEach((s) => {
    if (s.type === 'list') {
      (s.values || []).filter(Boolean).forEach((v) => thumbs.push(v));
    } else if (s.type === 'single' && s.value) {
      if (s.kind === 'image') thumbs.push(s.value);
      else badges.push(KIND_BADGE[s.kind] || '文件');
    }
  });
  // custom 槽（如情感合成的参考音色）形态各异，摘要里不猜，展开后仍可见。
  return { thumbs, badges };
};

const ConfigCollapse = ({ locked, title, slots = [], onNew, children }) => {
  const [expanded, setExpanded] = useState(false);

  if (!locked) return <>{children}</>;

  const { thumbs, badges } = summarize(slots);
  const extra = thumbs.length - MAX_THUMBS;

  return (
    <>
      <div className='m-config-summary'>
        <div
          className='m-config-summary-main'
          onClick={() => setExpanded((v) => !v)}
        >
          {thumbs.slice(0, MAX_THUMBS).map((url, i) => (
            <Image
              key={i}
              src={url}
              width={28}
              height={28}
              fit='cover'
              style={{ borderRadius: 5, flex: '0 0 auto' }}
            />
          ))}
          {extra > 0 && <span className='m-summary-badge'>+{extra}</span>}
          {badges.map((b, i) => (
            <span key={`b${i}`} className='m-summary-badge'>
              {b}
            </span>
          ))}
          <span className='m-summary-title'>{title}</span>
          {expanded ? (
            <UpOutline fontSize={10} />
          ) : (
            <DownOutline fontSize={10} />
          )}
        </div>
        <Button size='mini' fill='none' onClick={onNew}>
          新建会话
        </Button>
      </div>
      {expanded && children}
    </>
  );
};

export default ConfigCollapse;
