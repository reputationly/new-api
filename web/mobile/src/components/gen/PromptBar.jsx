import React, { useState } from 'react';
import { Button, TextArea } from 'antd-mobile';

import { usePromptOptimize } from '@classic/hooks/common/usePromptOptimize';

// 底部提示词输入条。extra 用于放置模式相关的上传按钮等。
//
// 给了 optimizeCategory/optimizeTab 就挂上「AI 优化提示词」，与网页端同一条链路、同一份
// 运营配置（没开总开关 / 没配优化模型 / 该 tab 单独关掉时按钮不出现）。空输入点按钮给
// 提示而不是禁用——一个说不清为什么点不动的灰按钮更让人困惑。优化在途时发送按钮置灰：
// 此刻发出去的还是没优化的原文。
//
// optimizeEngine / optimizeContext 是所选模型的引擎族与本次请求的输入形态，只有 H3 用得上
// （换一套带字段名的分段系统提示词，并告诉优化模型这次是 I2VA / L2VA / FL2VA、时长几秒）。
// 不传就是通用行为，图像与音乐两页不受影响。**别在这里判 H3**：本组件是三个体验区共用的
// 通用输入条，把引擎族的知识塞进来，下一个引擎就得再改它一次。
const PromptBar = ({
  onSend,
  generating,
  disabled = false,
  placeholder = '输入提示词…',
  allowEmpty = false,
  extra = null,
  optimizeCategory,
  optimizeTab,
  optimizeEngine,
  optimizeContext,
}) => {
  const [text, setText] = useState('');
  const {
    available: canOptimize,
    optimizing,
    optimize,
  } = usePromptOptimize(optimizeCategory, optimizeTab, {
    engine: optimizeEngine,
    context: optimizeContext,
  });

  const handleSend = () => {
    const value = text.trim();
    if (!value && !allowEmpty) return;
    onSend(value);
    setText('');
  };

  return (
    <div
      style={{
        borderTop: '1px solid var(--adm-color-border)',
        background: '#fff',
        padding: 8,
        paddingBottom: 'calc(8px + var(--safe-area-inset-bottom))',
      }}
    >
      {extra}
      {canOptimize && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            marginBottom: 6,
          }}
        >
          <Button
            size='mini'
            fill='none'
            color='primary'
            loading={optimizing}
            disabled={optimizing || generating}
            onClick={async () => {
              const out = await optimize(text);
              if (out) setText(out);
            }}
          >
            {optimizing ? '优化中…' : '✦ AI 优化提示词'}
          </Button>
          <span
            style={{
              fontSize: 11,
              color: optimizing
                ? 'var(--adm-color-warning)'
                : 'var(--adm-color-weak)',
            }}
          >
            {optimizing ? '请勿刷新或切换页面，否则要重来' : '结果会填回输入框'}
          </span>
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
        <div
          style={{
            flex: 1,
            background: 'var(--adm-color-fill-content, #f5f5f5)',
            borderRadius: 8,
            padding: '6px 10px',
          }}
        >
          <TextArea
            placeholder={placeholder}
            value={text}
            onChange={setText}
            rows={1}
            autoSize={{ minRows: 1, maxRows: 4 }}
          />
        </div>
        <Button
          color='primary'
          loading={generating}
          disabled={disabled || generating || optimizing}
          onClick={handleSend}
        >
          发送
        </Button>
      </div>
    </div>
  );
};

export default PromptBar;
