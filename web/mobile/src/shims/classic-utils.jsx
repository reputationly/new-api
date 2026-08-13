// web/classic/src/helpers/utils.jsx 的移动端替身。
// 原文件引入 Semi/react-toastify/render.js 等桌面依赖，无法直接复用；
// 此处用 antd-mobile Toast 实现提示函数，并"逐字拷贝"复用链路所需的纯函数。
// 拷贝部分注明来源，classic 侧对应函数变更时需同步（构建期 grep 校验见 M2）。
import { Toast as AmToast } from 'antd-mobile';

import {
  MESSAGE_ROLES,
  THINK_TAG_REGEX,
} from '@classic/constants/playground.constants';

const toastShow = (icon, content, durationMs = 3000) => {
  AmToast.show({ icon, content: String(content), duration: durationMs });
};

// 与 classic 的 handleUnauthorized 对应（那边跳 /login 靠 mobile-router 的 UA 规则转到
// /m/login，这里本就在移动端，直接跳 /m/login 省一次往返）。
//
// **必须导出**：@classic/helpers/api 的拦截器 import 了它，而 vite.config.js 的 SHIM_MAP
// 会把那句 './utils' 整模块重定向到本文件——漏了不是行为退化，是构建直接失败。
let redirectingToLogin = false;

export function handleUnauthorized() {
  localStorage.removeItem('user');
  // 登录/注册页上的 401 是正常的，再跳一次就成环了。
  if (
    /(^|\/)(login|register|reset|oauth)(\/|$)/.test(window.location.pathname)
  ) {
    return;
  }
  // 一个页面常并发多个请求，过期时会同时 401；只跳第一次。
  if (redirectingToLogin) return;
  redirectingToLogin = true;
  window.location.href = '/m/login?expired=true';
}

export function showError(error) {
  console.error(error);
  if (error && error.message) {
    if (error.name === 'AxiosError' && error.response) {
      switch (error.response.status) {
        case 401:
          handleUnauthorized();
          break;
        case 429:
          toastShow('fail', '错误：请求次数过多，请稍后再试！');
          break;
        case 500:
          toastShow('fail', '错误：服务器内部错误，请联系管理员！');
          break;
        case 405:
          toastShow(undefined, '本站仅作演示之用，无服务端！');
          break;
        default:
          toastShow('fail', '错误：' + error.message);
      }
      return;
    }
    toastShow('fail', '错误：' + error.message);
  } else {
    toastShow('fail', '错误：' + error);
  }
}

export function showWarning(message) {
  toastShow('fail', message);
}

export function showSuccess(message) {
  toastShow('success', message, 2000);
}

export function showInfo(message) {
  toastShow(undefined, message);
}

export function showNotice(message) {
  toastShow(undefined, message, 4000);
}

// ---- 以下均拷贝自 web/classic/src/helpers/utils.jsx，保持行为一致 ----

export function isAdmin() {
  let user = localStorage.getItem('user');
  if (!user) return false;
  user = JSON.parse(user);
  return user.role >= 10;
}

export function isRoot() {
  let user = localStorage.getItem('user');
  if (!user) return false;
  user = JSON.parse(user);
  return user.role >= 100;
}

export function getSystemName() {
  let system_name = localStorage.getItem('system_name');
  if (!system_name) return 'New API';
  return system_name;
}

export function getLogo() {
  let logo = localStorage.getItem('logo');
  if (!logo) return '/logo.png';
  return logo;
}

export function getUserIdFromLocalStorage() {
  let user = localStorage.getItem('user');
  if (!user) return -1;
  user = JSON.parse(user);
  return user.id;
}

export function containsCJK(str) {
  return /[一-鿿㐀-䶿]/.test(str || '');
}

export async function copy(text) {
  let okay = true;
  try {
    await navigator.clipboard.writeText(text);
  } catch (e) {
    try {
      const textarea = window.document.createElement('textarea');
      textarea.value = text;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.left = '-9999px';
      textarea.style.top = '-9999px';
      window.document.body.appendChild(textarea);
      textarea.select();
      window.document.execCommand('copy');
      window.document.body.removeChild(textarea);
    } catch (e2) {
      okay = false;
      console.error(e2);
    }
  }
  return okay;
}

let messageId = 4;
export const generateMessageId = () => `${messageId++}`;

export const getTextContent = (message) => {
  if (!message || !message.content) return '';

  if (Array.isArray(message.content)) {
    const textContent = message.content.find((item) => item.type === 'text');
    return textContent?.text || '';
  }
  return typeof message.content === 'string' ? message.content : '';
};

export const processThinkTags = (content, reasoningContent = '') => {
  if (!content || !content.includes('<think>')) {
    return { content, reasoningContent };
  }

  const thoughts = [];
  const replyParts = [];
  let lastIndex = 0;
  let match;

  THINK_TAG_REGEX.lastIndex = 0;
  while ((match = THINK_TAG_REGEX.exec(content)) !== null) {
    replyParts.push(content.substring(lastIndex, match.index));
    thoughts.push(match[1]);
    lastIndex = match.index + match[0].length;
  }
  replyParts.push(content.substring(lastIndex));

  const processedContent = replyParts
    .join('')
    .replace(/<\/?think>/g, '')
    .trim();
  const thoughtsStr = thoughts.join('\n\n---\n\n');
  const processedReasoningContent =
    reasoningContent && thoughtsStr
      ? `${reasoningContent}\n\n---\n\n${thoughtsStr}`
      : reasoningContent || thoughtsStr;

  return {
    content: processedContent,
    reasoningContent: processedReasoningContent,
  };
};

export const processIncompleteThinkTags = (content, reasoningContent = '') => {
  if (!content) return { content: '', reasoningContent };

  const lastOpenThinkIndex = content.lastIndexOf('<think>');
  if (lastOpenThinkIndex === -1) {
    return processThinkTags(content, reasoningContent);
  }

  const fragmentAfterLastOpen = content.substring(lastOpenThinkIndex);
  if (!fragmentAfterLastOpen.includes('</think>')) {
    const unclosedThought = fragmentAfterLastOpen
      .substring('<think>'.length)
      .trim();
    const cleanContent = content.substring(0, lastOpenThinkIndex);
    const processedReasoningContent = unclosedThought
      ? reasoningContent
        ? `${reasoningContent}\n\n---\n\n${unclosedThought}`
        : unclosedThought
      : reasoningContent;

    return processThinkTags(cleanContent, processedReasoningContent);
  }

  return processThinkTags(content, reasoningContent);
};

export const buildMessageContent = (
  textContent,
  imageUrls = [],
  imageEnabled = false,
) => {
  if (!textContent && (!imageUrls || imageUrls.length === 0)) {
    return '';
  }

  const validImageUrls = imageUrls.filter((url) => url && url.trim() !== '');

  if (imageEnabled && validImageUrls.length > 0) {
    return [
      { type: 'text', text: textContent || '' },
      ...validImageUrls.map((url) => ({
        type: 'image_url',
        image_url: { url: url.trim() },
      })),
    ];
  }

  return textContent || '';
};

export const createMessage = (role, content, options = {}) => ({
  role,
  content,
  createAt: Date.now(),
  id: generateMessageId(),
  ...options,
});

export const createLoadingAssistantMessage = () =>
  createMessage(MESSAGE_ROLES.ASSISTANT, '', {
    reasoningContent: '',
    isReasoningExpanded: true,
    isThinkingComplete: false,
    hasAutoCollapsed: false,
    status: 'loading',
  });

// 从出站正文里剔除 <think> 段。content 可能是字符串,也可能是多模态数组(见
// buildMessageContent),后者只处理 text 项,图片原样带走。
const stripThinkForAPI = (content) => {
  if (typeof content === 'string') {
    return processIncompleteThinkTags(content, '').content;
  }
  if (Array.isArray(content)) {
    return content.map((item) =>
      item?.type === 'text'
        ? {
            ...item,
            text: processIncompleteThinkTags(item.text || '', '').content,
          }
        : item,
    );
  }
  return content;
};

// 助手的思考不回传:同一段信息,上游用 reasoning_content 字段送来时本函数天然丢掉
// (只取 role/content),用正文内联 <think> 送来时却原样带回去——同样的东西因传输形式
// 不同而区别对待,说不通。多数推理模型也明确要求别把思考塞回上下文,且它会逐轮累积、
// 重复计费。只处理 assistant:用户自己在提问里打的 <think> 是正经内容,不能动。
//
// 注意:本函数不是死代码。移动端 Chat.jsx 用的是 @classic/helpers/api 的 buildApiPayload,
// 而 api.js 里那句 `import { formatMessageForAPI } from './utils'` 会被 vite.config.js
// 的 SHIM_MAP 按绝对路径整模块重定向到本文件——grep 移动端源码搜不到调用点,但它确实在跑。
// 改 classic 那份而漏了这份,桌面端会好、移动端照旧。
export const formatMessageForAPI = (message) => {
  if (!message) return null;

  return {
    role: message.role,
    content:
      message.role === MESSAGE_ROLES.ASSISTANT
        ? stripThinkForAPI(message.content)
        : message.content,
  };
};

export const isValidMessage = (message) => {
  return message && message.role && (message.content || message.content === '');
};
