import React from 'react';
import { Toast } from 'antd-mobile';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { copyToClipboard } from '../../utils/share';

// 助手消息的 markdown 渲染。只在消息落到 COMPLETE 时启用,流式过程中调用方仍走 pre-wrap
// 原样输出(见 Chat.jsx renderAssistant)。这样一次绕开两个问题:每来一个 token 就重新
// parse 整段的手机端性能开销,以及流式中途 `**` / ``` 未闭合造成的渲染跳变闪烁。
// INCOMPLETE 是流式进行中的状态(见 useApiRequest.jsx:82,123),不是「被中断的终态」——
// 用户点停止生成同样会走 completeMessage() 落到 COMPLETE,所以终态判定只认 COMPLETE。
//
// 刻意不引 rehype-raw:模型输出里的 HTML 一律按纯文本处理,顺手消掉 XSS 面。
// 刻意不做语法高亮:highlight.js / shiki 的语言包是体积大头,而手机上看代码的核心诉求是
// 「能复制走」和「不折行」,配色优先级低,等有人抱怨再加。

const REMARK_PLUGINS = [remarkGfm];

// 从渲染树里抽纯文本:代码块要复制的是原文,不是渲染后的 DOM。
const nodeText = (node) => {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join('');
  if (node.props?.children !== undefined) return nodeText(node.props.children);
  return '';
};

const CodeBlock = ({ text }) => {
  const handleCopy = async () => {
    // 复制动作直接挂在点击回调上,前面没有 await —— 还在用户手势上下文里,
    // iOS 与微信 WebView 才不会拒绝写剪贴板(ShareBar 那边的踩坑记录同理)。
    if (await copyToClipboard(text)) {
      Toast.show({ content: '已复制' });
    } else {
      Toast.show({ content: '复制失败，请长按选中' });
    }
  };

  return (
    // 复制按钮走顶部工具条而不是悬浮在右上角:窄屏上悬浮按钮会压住首行代码,而且手机
    // 没有 hover,靠悬停才显形的按钮等于没有。
    <div className='m-md-code'>
      <div className='m-md-code-bar'>
        <button type='button' className='m-md-copy' onClick={handleCopy}>
          复制
        </button>
      </div>
      <pre>
        <code>{text}</code>
      </pre>
    </div>
  );
};

const COMPONENTS = {
  // pre 不渲染 children,取原文自绘 —— 于是块级 code 永远不会走到下面的 code 覆盖,
  // code 就只剩行内一种情况要处理,省掉 react-markdown v9+ 去掉 inline prop 之后
  // 还要靠父节点判断块级/行内的那套麻烦。
  pre: ({ children }) => <CodeBlock text={nodeText(children)} />,
  code: ({ children }) => <code className='m-md-inline-code'>{children}</code>,
  // 外链新窗口打开并断掉 opener 引用。
  a: ({ href, children }) => (
    <a href={href} target='_blank' rel='noopener noreferrer'>
      {children}
    </a>
  ),
  // 表格在窄屏上必然溢出,包一层横向滚动容器,而不是让它把气泡撑破。
  table: ({ children }) => (
    <div className='m-md-table-wrap'>
      <table>{children}</table>
    </div>
  ),
};

const MarkdownMessage = ({ children }) => (
  <div className='m-md'>
    <Markdown remarkPlugins={REMARK_PLUGINS} components={COMPONENTS}>
      {children}
    </Markdown>
  </div>
);

export default MarkdownMessage;
