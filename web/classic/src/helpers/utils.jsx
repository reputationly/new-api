/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import { Toast, Pagination } from '@douyinfe/semi-ui';
import {
  toastConstants,
  BILLING_PRICING_VARS,
  BILLING_VAR_REGEX,
} from '../constants';
import { getQuotaPerUnit } from './quota';
import React from 'react';
import { toast } from 'react-toastify';
import {
  THINK_TAG_REGEX,
  MESSAGE_ROLES,
} from '../constants/playground.constants';
import { TABLE_COMPACT_MODES_KEY } from '../constants';
import { MOBILE_BREAKPOINT } from '../hooks/common/useIsMobile';
// 纯计算部分拆出去的两个模块（不引 UI 依赖，手机端可直接 import，不必手抄）。
// 必须 import 进来而不是 `export ... from`：本文件内部要调用它们，
// 纯重导出不会在模块作用域建立绑定，运行时抛 ReferenceError 且构建期不报错。
import { buildLoginUrl } from './authRedirect';
import { formatPriceWithCeiling } from './priceFormat';
import {
  videoResolutionRank,
  videoSecondsRank,
  flattenVideoMatrix,
} from './videoMatrix';

const HTMLToastContent = ({ htmlContent }) => {
  return <div dangerouslySetInnerHTML={{ __html: htmlContent }} />;
};
export default HTMLToastContent;
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

// 是否包含中日韩(CJK)字符。用于判定提示词是否需要中译英。
// 覆盖基本汉字(一-鿿)与扩展 A(㐀-䶿);日文假名不在音效场景内,按需扩展。
export function containsCJK(str) {
  return /[一-鿿㐀-䶿]/.test(str || '');
}

export function getFooterHTML() {
  return localStorage.getItem('footer_html');
}

export async function copy(text) {
  let okay = true;
  try {
    await navigator.clipboard.writeText(text);
  } catch (e) {
    try {
      // 构建 textarea 执行复制命令，保留多行文本格式
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
    } catch (e) {
      okay = false;
      console.error(e);
    }
  }
  return okay;
}

// isMobile 函数已移除，请改用 useIsMobile Hook

let showErrorOptions = { autoClose: toastConstants.ERROR_TIMEOUT };
let showWarningOptions = { autoClose: toastConstants.WARNING_TIMEOUT };
let showSuccessOptions = { autoClose: toastConstants.SUCCESS_TIMEOUT };
let showInfoOptions = { autoClose: toastConstants.INFO_TIMEOUT };
let showNoticeOptions = { autoClose: false };

const isMobileScreen = window.matchMedia(
  `(max-width: ${MOBILE_BREAKPOINT - 1}px)`,
).matches;
if (isMobileScreen) {
  showErrorOptions.position = 'top-center';
  // showErrorOptions.transition = 'flip';

  showSuccessOptions.position = 'top-center';
  // showSuccessOptions.transition = 'flip';

  showInfoOptions.position = 'top-center';
  // showInfoOptions.transition = 'flip';

  showNoticeOptions.position = 'top-center';
  // showNoticeOptions.transition = 'flip';
}

// 登录已过期(会话 cookie 到期)时统一收口：清本地登录态并跳登录页。
//
// 前端的登录态是 localStorage['user']，而会话 cookie 是 HttpOnly——JS 感知不到它没了，
// 于是页面看着还登着，一操作才冒一句「无权进行此操作，未登录且未提供 access token」
// (i18n auth.not_logged_in，后端 401)。这句话对用户毫无信息量，直接送去重新登录才对。
//
// 抽成函数是因为有两个入口：axios 拦截器(所有请求，含 skipErrorHandler 的)与 showError
// (少数直接拿着 AxiosError 调用的地方)。两处必须同一套行为，否则又会分叉。
let redirectingToLogin = false;

export function handleUnauthorized() {
  // 本地登录态先清掉：即使下面因为已在登录页而不跳转，也不该留着一个假的已登录状态。
  localStorage.removeItem('user');
  // 登录/注册/找回/OAuth 回调页上的 401 是正常的(比如登录前探测身份)，再跳一次就成环了。
  if (
    /(^|\/)(login|register|reset|oauth)(\/|$)/.test(window.location.pathname)
  ) {
    return;
  }
  // 一个页面常常并发好几个请求，过期时会同时 401；只跳第一次。
  if (redirectingToLogin) return;
  redirectingToLogin = true;
  // 手机端(basename /m)不用单独处理：/login 命中 mobile-router 的 UA 跳转规则，
  // 会被送到 /m/login，query 也一并保留（mobile-router.go 的 :99-101）。
  // ?expired=true 由登录页读出来提示「登录已过期」，?redirect= 用来登录后回到原页。
  window.location.href = buildLoginUrl('/login');
}

export function showError(error) {
  console.error(error);
  if (error.message) {
    if (error.name === 'AxiosError') {
      // 网络错误(超时/断网)时没有 response，直接读 .status 会抛 TypeError，
      // 把原本要展示的错误变成一个控制台异常。
      switch (error.response?.status) {
        case 401:
          handleUnauthorized();
          break;
        case 429:
          Toast.error('错误：请求次数过多，请稍后再试！');
          break;
        case 500:
          Toast.error('错误：服务器内部错误，请联系管理员！');
          break;
        case 405:
          Toast.info('本站仅作演示之用，无服务端！');
          break;
        default:
          Toast.error('错误：' + error.message);
      }
      return;
    }
    Toast.error('错误：' + error.message);
  } else {
    Toast.error('错误：' + error);
  }
}

export function showWarning(message) {
  Toast.warning(message);
}

export function showSuccess(message) {
  Toast.success(message);
}

export function showInfo(message) {
  Toast.info(message);
}

export function showNotice(message, isHTML = false) {
  if (isHTML) {
    toast(<HTMLToastContent htmlContent={message} />, showNoticeOptions);
  } else {
    Toast.info(message);
  }
}

export function openPage(url) {
  window.open(url);
}

export function removeTrailingSlash(url) {
  if (!url) return '';
  if (url.endsWith('/')) {
    return url.slice(0, -1);
  } else {
    return url;
  }
}

export function getTodayStartTimestamp() {
  var now = new Date();
  now.setHours(0, 0, 0, 0);
  return Math.floor(now.getTime() / 1000);
}

export function timestamp2string(timestamp) {
  let date = new Date(timestamp * 1000);
  let year = date.getFullYear().toString();
  let month = (date.getMonth() + 1).toString();
  let day = date.getDate().toString();
  let hour = date.getHours().toString();
  let minute = date.getMinutes().toString();
  let second = date.getSeconds().toString();
  if (month.length === 1) {
    month = '0' + month;
  }
  if (day.length === 1) {
    day = '0' + day;
  }
  if (hour.length === 1) {
    hour = '0' + hour;
  }
  if (minute.length === 1) {
    minute = '0' + minute;
  }
  if (second.length === 1) {
    second = '0' + second;
  }
  return (
    year + '-' + month + '-' + day + ' ' + hour + ':' + minute + ':' + second
  );
}

export function timestamp2string1(
  timestamp,
  dataExportDefaultTime = 'hour',
  showYear = false,
) {
  let date = new Date(timestamp * 1000);
  let year = date.getFullYear();
  let month = (date.getMonth() + 1).toString();
  let day = date.getDate().toString();
  let hour = date.getHours().toString();
  if (month.length === 1) {
    month = '0' + month;
  }
  if (day.length === 1) {
    day = '0' + day;
  }
  if (hour.length === 1) {
    hour = '0' + hour;
  }
  // 仅在跨年时显示年份
  let str = showYear ? year + '-' + month + '-' + day : month + '-' + day;
  if (dataExportDefaultTime === 'hour') {
    str += ' ' + hour + ':00';
  } else if (dataExportDefaultTime === 'week') {
    let nextWeek = new Date(timestamp * 1000 + 6 * 24 * 60 * 60 * 1000);
    let nextWeekYear = nextWeek.getFullYear();
    let nextMonth = (nextWeek.getMonth() + 1).toString();
    let nextDay = nextWeek.getDate().toString();
    if (nextMonth.length === 1) {
      nextMonth = '0' + nextMonth;
    }
    if (nextDay.length === 1) {
      nextDay = '0' + nextDay;
    }
    // 周视图结束日期也仅在跨年时显示年份
    let nextStr = showYear
      ? nextWeekYear + '-' + nextMonth + '-' + nextDay
      : nextMonth + '-' + nextDay;
    str += ' - ' + nextStr;
  }
  return str;
}

// 检查时间戳数组是否跨年
export function isDataCrossYear(timestamps) {
  if (!timestamps || timestamps.length === 0) return false;
  const years = new Set(
    timestamps.map((ts) => new Date(ts * 1000).getFullYear()),
  );
  return years.size > 1;
}

export function downloadTextAsFile(text, filename) {
  let blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  let url = URL.createObjectURL(blob);
  let a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
}

export const verifyJSON = (str) => {
  try {
    JSON.parse(str);
  } catch (e) {
    return false;
  }
  return true;
};

export function verifyJSONPromise(value) {
  try {
    JSON.parse(value);
    return Promise.resolve();
  } catch (e) {
    return Promise.reject('不是合法的 JSON 字符串');
  }
}

export function shouldShowPrompt(id) {
  let prompt = localStorage.getItem(`prompt-${id}`);
  return !prompt;
}

export function setPromptShown(id) {
  localStorage.setItem(`prompt-${id}`, 'true');
}

/**
 * 比较两个对象的属性，找出有变化的属性，并返回包含变化属性信息的数组
 * @param {Object} oldObject - 旧对象
 * @param {Object} newObject - 新对象
 * @return {Array} 包含变化属性信息的数组，每个元素是一个对象，包含 key, oldValue 和 newValue
 */
export function compareObjects(oldObject, newObject) {
  const changedProperties = [];

  // 比较两个对象的属性
  for (const key in oldObject) {
    if (oldObject.hasOwnProperty(key) && newObject.hasOwnProperty(key)) {
      if (oldObject[key] !== newObject[key]) {
        changedProperties.push({
          key: key,
          oldValue: oldObject[key],
          newValue: newObject[key],
        });
      }
    }
  }

  return changedProperties;
}

// playground message

// 生成唯一ID
let messageId = 4;
export const generateMessageId = () => `${messageId++}`;

// 提取消息中的文本内容
export const getTextContent = (message) => {
  if (!message || !message.content) return '';

  if (Array.isArray(message.content)) {
    const textContent = message.content.find((item) => item.type === 'text');
    return textContent?.text || '';
  }
  return typeof message.content === 'string' ? message.content : '';
};

// 处理 think 标签
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

// 处理未完成的 think 标签
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

// 构建消息内容（包含图片）
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

// 创建新消息
export const createMessage = (role, content, options = {}) => ({
  role,
  content,
  createAt: Date.now(),
  id: generateMessageId(),
  ...options,
});

// 创建加载中的助手消息
export const createLoadingAssistantMessage = () =>
  createMessage(MESSAGE_ROLES.ASSISTANT, '', {
    reasoningContent: '',
    isReasoningExpanded: true,
    isThinkingComplete: false,
    hasAutoCollapsed: false,
    status: 'loading',
  });

// 检查消息是否包含图片
export const hasImageContent = (message) => {
  return (
    message &&
    Array.isArray(message.content) &&
    message.content.some((item) => item.type === 'image_url')
  );
};

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

// 格式化消息用于API请求
// 助手的思考不回传:同一段信息,上游用 reasoning_content 字段送来时本函数天然丢掉
// (只取 role/content),用正文内联 <think> 送来时却原样带回去——同样的东西因传输形式
// 不同而区别对待,说不通。多数推理模型也明确要求别把思考塞回上下文,且它会逐轮累积、
// 重复计费。只处理 assistant:用户自己在提问里打的 <think> 是正经内容,不能动。
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

// 验证消息是否有效
export const isValidMessage = (message) => {
  return message && message.role && (message.content || message.content === '');
};

// 获取最后一条用户消息
export const getLastUserMessage = (messages) => {
  if (!Array.isArray(messages)) return null;

  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === MESSAGE_ROLES.USER) {
      return messages[i];
    }
  }
  return null;
};

// 获取最后一条助手消息
export const getLastAssistantMessage = (messages) => {
  if (!Array.isArray(messages)) return null;

  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === MESSAGE_ROLES.ASSISTANT) {
      return messages[i];
    }
  }
  return null;
};

// 计算相对时间（几天前、几小时前等）
export const getRelativeTime = (publishDate) => {
  if (!publishDate) return '';

  const now = new Date();
  const pubDate = new Date(publishDate);

  // 如果日期无效，返回原始字符串
  if (isNaN(pubDate.getTime())) return publishDate;

  const diffMs = now.getTime() - pubDate.getTime();
  const diffSeconds = Math.floor(diffMs / 1000);
  const diffMinutes = Math.floor(diffSeconds / 60);
  const diffHours = Math.floor(diffMinutes / 60);
  const diffDays = Math.floor(diffHours / 24);
  const diffWeeks = Math.floor(diffDays / 7);
  const diffMonths = Math.floor(diffDays / 30);
  const diffYears = Math.floor(diffDays / 365);

  // 如果是未来时间，显示具体日期
  if (diffMs < 0) {
    return formatDateString(pubDate);
  }

  // 根据时间差返回相应的描述
  if (diffSeconds < 60) {
    return '刚刚';
  } else if (diffMinutes < 60) {
    return `${diffMinutes} 分钟前`;
  } else if (diffHours < 24) {
    return `${diffHours} 小时前`;
  } else if (diffDays < 7) {
    return `${diffDays} 天前`;
  } else if (diffWeeks < 4) {
    return `${diffWeeks} 周前`;
  } else if (diffMonths < 12) {
    return `${diffMonths} 个月前`;
  } else if (diffYears < 2) {
    return '1 年前';
  } else {
    // 超过2年显示具体日期
    return formatDateString(pubDate);
  }
};

// 格式化日期字符串
export const formatDateString = (date) => {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
};

// 格式化日期时间字符串（包含时间）
export const formatDateTimeString = (date) => {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}`;
};

function readTableCompactModes() {
  try {
    const json = localStorage.getItem(TABLE_COMPACT_MODES_KEY);
    return json ? JSON.parse(json) : {};
  } catch {
    return {};
  }
}

function writeTableCompactModes(modes) {
  try {
    localStorage.setItem(TABLE_COMPACT_MODES_KEY, JSON.stringify(modes));
  } catch {
    // ignore
  }
}

export function getTableCompactMode(tableKey = 'global') {
  const modes = readTableCompactModes();
  return !!modes[tableKey];
}

export function setTableCompactMode(compact, tableKey = 'global') {
  const modes = readTableCompactModes();
  modes[tableKey] = compact;
  writeTableCompactModes(modes);
}

// -------------------------------
// Select 组件统一过滤逻辑
// 使用方式： <Select filter={selectFilter} ... />
// 统一的 Select 搜索过滤逻辑 -- 支持同时匹配 option.value 与 option.label
export const selectFilter = (input, option) => {
  if (!input) return true;

  const keyword = input.trim().toLowerCase();
  const valueText = (option?.value ?? '').toString().toLowerCase();
  const labelText = (option?.label ?? '').toString().toLowerCase();

  return valueText.includes(keyword) || labelText.includes(keyword);
};

// -------------------------------
// 模型定价计算工具函数
// 实现挪到 ./priceFormat，好让手机端直接复用而不是抄一份
// （utils.jsx 会传染桌面依赖、在 mobile 侧被整模块 shim 掉）。
//
// 见文件头 import：必须 import 再 export，`export { X } from './x'` 是纯重导出、
// 不建立本地绑定，而本文件内部还要调用它（下面三处）。
export { formatPriceWithCeiling };

export const getModelPricingCurrencyConfig = () => {
  const quotaDisplayType = localStorage.getItem('quota_display_type') || 'USD';
  let symbol = '$';
  let rate = quotaDisplayType === 'CNY' ? 7.3 : 1;

  try {
    const status = JSON.parse(localStorage.getItem('status') || '{}');
    if (quotaDisplayType === 'CNY') {
      symbol = '¥';
      rate = status?.usd_exchange_rate || 7.3;
    } else if (quotaDisplayType === 'CUSTOM') {
      symbol = status?.custom_currency_symbol || '¤';
      rate = status?.custom_currency_exchange_rate || 1;
    }
  } catch (e) {}

  return { symbol, rate };
};

/**
 * 取某个分组下某个模型的**实际**倍率。
 *
 * 分组倍率不再是整组一个数：管理员可以给分组内单个模型配折扣（分组管理 → 模型折扣）。
 * 后端在 /api/pricing 的 group_model_ratio 里下发**已展开通配、已算完三层**的终值，
 * 前端只做这一步查表——通配匹配放在前端就要在 classic / default / mobile 各写一遍，
 * 那是三份可能算错的价。
 *
 * @param {Record<string, number>} groupRatio 分组基础倍率
 * @param {Record<string, Record<string, number>>} groupModelRatio 分组 → 模型 → 终值倍率
 */
export const getEffectiveGroupRatio = (
  groupRatio,
  groupModelRatio,
  group,
  modelName,
) => {
  const perModel = groupModelRatio?.[group]?.[modelName];
  return perModel !== undefined ? perModel : groupRatio?.[group];
};

export const calculateModelPrice = ({
  record,
  selectedGroup,
  groupRatio,
  groupModelRatio,
  tokenUnit,
  displayPrice,
  quotaDisplayType = 'USD',
  precision = 2,
  pointsEnabled = false,
  quotaPerPoint = 0,
  pointsEnabledGroups = [],
  // 允许积分抵扣的模型名单。null = 后端未启用渠道白名单，只按分组判（旧口径）。
  // 分组合并之后单看分组会把外采模型也标成「可用积分」，实际扣的是余额。
  pointsEnabledModels = null,
}) => {
  // 1. 选择实际使用的分组
  let usedGroup = selectedGroup;
  let usedGroupRatio = getEffectiveGroupRatio(
    groupRatio,
    groupModelRatio,
    selectedGroup,
    record.model_name,
  );

  if (selectedGroup === 'all' || usedGroupRatio === undefined) {
    // 在模型可用分组中选择倍率最小的分组，若无则使用 1
    //
    // 比的必须是**该模型在各分组下的实际倍率**而不是分组基础倍率：配了模型折扣后
    // 两者会分叉，拿基础倍率挑出来的「最优分组」可能根本不是最便宜的那个。
    let minRatio = Number.POSITIVE_INFINITY;
    if (
      Array.isArray(record.enable_groups) &&
      record.enable_groups.length > 0
    ) {
      record.enable_groups.forEach((g) => {
        const r = getEffectiveGroupRatio(
          groupRatio,
          groupModelRatio,
          g,
          record.model_name,
        );
        if (r !== undefined && r < minRatio) {
          minRatio = r;
          usedGroup = g;
          usedGroupRatio = r;
        }
      });
    }

    // 如果找不到合适分组倍率，回退为 1
    if (usedGroupRatio === undefined) {
      usedGroupRatio = 1;
    }
  }

  // 2. 动态计费（tiered_expr）
  if (record.billing_mode === 'tiered_expr' && record.billing_expr) {
    return {
      isDynamicPricing: true,
      billingExpr: record.billing_expr,
      usedGroup,
      usedGroupRatio,
    };
  }

  // 2.5 视频计费矩阵：实收由「分辨率 × 输入是否含视频」查表决定，与 model_ratio 无关。
  // 不短路的话定价页会展示那个预扣锚点（480p 实际 ¥46 却显示 ¥51），还会多出一个
  // 对视频模型毫无意义的「输出价格」。见 docs/video-billing-matrix-design.md §2.6。
  if (record.video_pricing?.mode) {
    return {
      isVideoMatrix: true,
      videoPricing: record.video_pricing,
      usedGroup,
      usedGroupRatio,
    };
  }

  // 3. 根据计费类型计算价格
  if (record.quota_type === 0) {
    // 按量计费
    const isTokensDisplay = quotaDisplayType === 'TOKENS';
    const inputRatioPriceUSD = record.model_ratio * 2 * usedGroupRatio;
    const unitDivisor = tokenUnit === 'K' ? 1000 : 1;
    const unitLabel = tokenUnit === 'K' ? 'K' : 'M';
    const hasRatioValue = (value) =>
      value !== undefined &&
      value !== null &&
      value !== '' &&
      Number.isFinite(Number(value));

    const formatRatio = (value) =>
      hasRatioValue(value) ? Number(Number(value).toFixed(6)) : null;

    if (isTokensDisplay) {
      return {
        inputRatio: formatRatio(record.model_ratio),
        completionRatio: formatRatio(record.completion_ratio),
        cacheRatio: formatRatio(record.cache_ratio),
        createCacheRatio: formatRatio(record.create_cache_ratio),
        imageRatio: formatRatio(record.image_ratio),
        audioInputRatio: formatRatio(record.audio_ratio),
        audioOutputRatio: formatRatio(record.audio_completion_ratio),
        isPerToken: true,
        isTokensDisplay: true,
        usedGroup,
        usedGroupRatio,
      };
    }

    const formatTokenPrice = (priceUSD) => {
      const rawDisplayPrice = displayPrice(priceUSD, 12);
      const symbol = rawDisplayPrice.replace(/[-\d.,\s]/g, '');
      const numericPrice =
        Number(rawDisplayPrice.replace(/[^0-9.-]/g, '')) / unitDivisor;
      return `${symbol}${formatPriceWithCeiling(numericPrice, precision)}`;
    };

    const inputPrice = formatTokenPrice(inputRatioPriceUSD);
    const audioInputPrice = hasRatioValue(record.audio_ratio)
      ? formatTokenPrice(inputRatioPriceUSD * Number(record.audio_ratio))
      : null;

    // 积分价：仅当积分启用且该模型实际使用的分组在白名单时，按 quota unit 换算积分数。
    // 返回原始数值不取整——渲染点统一格式化：(0,1) 显示「<1 积分」（结算每笔至少烧
    // 1 积分，floor 成 0 会造成免费假象），≥1 floor 取整（积分不显示小数）
    // 模型这层只在后端启用了渠道白名单时才生效（pointsEnabledModels 非 null），
    // 否则维持只看分组的旧口径。
    const modelAllowsPoints =
      !Array.isArray(pointsEnabledModels) ||
      pointsEnabledModels.includes(record.model_name);
    const usdToPoints = (usd) =>
      pointsEnabled &&
      quotaPerPoint > 0 &&
      Array.isArray(pointsEnabledGroups) &&
      pointsEnabledGroups.includes(usedGroup) &&
      modelAllowsPoints
        ? (usd * getQuotaPerUnit()) / quotaPerPoint / unitDivisor
        : null;
    const pointsMap = {
      input: usdToPoints(inputRatioPriceUSD),
      completion: usdToPoints(
        inputRatioPriceUSD * Number(record.completion_ratio),
      ),
      cache: hasRatioValue(record.cache_ratio)
        ? usdToPoints(inputRatioPriceUSD * Number(record.cache_ratio))
        : null,
      'create-cache': hasRatioValue(record.create_cache_ratio)
        ? usdToPoints(inputRatioPriceUSD * Number(record.create_cache_ratio))
        : null,
      image: hasRatioValue(record.image_ratio)
        ? usdToPoints(inputRatioPriceUSD * Number(record.image_ratio))
        : null,
      'audio-input': hasRatioValue(record.audio_ratio)
        ? usdToPoints(inputRatioPriceUSD * Number(record.audio_ratio))
        : null,
      'audio-output':
        hasRatioValue(record.audio_ratio) &&
        hasRatioValue(record.audio_completion_ratio)
          ? usdToPoints(
              inputRatioPriceUSD *
                Number(record.audio_ratio) *
                Number(record.audio_completion_ratio),
            )
          : null,
    };

    return {
      inputPrice,
      points: pointsMap,
      completionPrice: formatTokenPrice(
        inputRatioPriceUSD * Number(record.completion_ratio),
      ),
      cachePrice: hasRatioValue(record.cache_ratio)
        ? formatTokenPrice(inputRatioPriceUSD * Number(record.cache_ratio))
        : null,
      createCachePrice: hasRatioValue(record.create_cache_ratio)
        ? formatTokenPrice(
            inputRatioPriceUSD * Number(record.create_cache_ratio),
          )
        : null,
      imagePrice: hasRatioValue(record.image_ratio)
        ? formatTokenPrice(inputRatioPriceUSD * Number(record.image_ratio))
        : null,
      audioInputPrice,
      audioOutputPrice:
        audioInputPrice && hasRatioValue(record.audio_completion_ratio)
          ? formatTokenPrice(
              inputRatioPriceUSD *
                Number(record.audio_ratio) *
                Number(record.audio_completion_ratio),
            )
          : null,
      unitLabel,
      isPerToken: true,
      isTokensDisplay: false,
      usedGroup,
      usedGroupRatio,
    };
  }

  if (record.quota_type === 1) {
    // 按次计费
    const priceUSD = parseFloat(record.model_price) * usedGroupRatio;
    const rawDisplayPrice = displayPrice(priceUSD, 12);
    const symbol = rawDisplayPrice.replace(/[-\d.,\s]/g, '');
    const numericPrice = Number(rawDisplayPrice.replace(/[^0-9.-]/g, ''));
    const displayVal = `${symbol}${formatPriceWithCeiling(
      numericPrice,
      precision,
    )}`;

    return {
      price: displayVal,
      points: {
        // 按次价 ceil：一次调用一次结算，实际就烧 ceil(积分价) 个整积分，
        // ceil 显示的即真实扣费（floor 会把不足 1 积分的按次价显示成免费）
        fixed:
          pointsEnabled &&
          quotaPerPoint > 0 &&
          Array.isArray(pointsEnabledGroups) &&
          pointsEnabledGroups.includes(usedGroup)
            ? Math.ceil((priceUSD * getQuotaPerUnit()) / quotaPerPoint)
            : null,
      },
      isPerToken: false,
      isTokensDisplay: false,
      usedGroup,
      usedGroupRatio,
    };
  }

  // 未知计费类型，返回占位信息
  return {
    price: '-',
    isPerToken: false,
    isTokensDisplay: false,
    usedGroup,
    usedGroupRatio,
  };
};

export const getModelPriceItems = (priceData, t, quotaDisplayType = 'USD') => {
  // 在每个货币价格项后追加对应「积分价」行（仅当该模型分组可积分消费，§8bis）
  const appendPoints = (items) => {
    const pm = priceData.points;
    if (!pm) return items;
    const out = [];
    items.forEach((item) => {
      out.push(item);
      const p = pm[item.key];
      if (p !== null && p !== undefined) {
        // 积分永远整数显示：(0,1) 显示「<1」防免费假象（结算每笔至少烧 1 积分），
        // 其余 floor；真正免费(0)如实显示 0（quota 成本为 0 时结算不烧积分）
        const n = Number(p);
        const pointsText =
          n > 0 && n < 1 ? '<1' : Math.floor(n).toLocaleString();
        out.push({
          key: `${item.key}-points`,
          label: t('积分价'),
          value: `${pointsText} ${t('积分')}`,
          suffix: item.suffix,
          isPoints: true,
        });
      }
    });
    return out;
  };

  if (priceData.isDynamicPricing) {
    return [
      {
        key: 'dynamic',
        label: t('动态计费'),
        value: '',
        suffix: '',
        isDynamic: true,
      },
    ];
  }

  if (priceData.isVideoMatrix) {
    return [
      {
        key: 'video-matrix',
        label: t('场景计费'),
        value: '',
        suffix: '',
        isVideoMatrix: true,
      },
    ];
  }

  if (priceData.isPerToken) {
    if (quotaDisplayType === 'TOKENS' || priceData.isTokensDisplay) {
      return [
        {
          key: 'input-ratio',
          label: t('输入倍率'),
          value: priceData.inputRatio,
          suffix: 'x',
        },
        {
          key: 'completion-ratio',
          label: t('补全倍率'),
          value: priceData.completionRatio,
          suffix: 'x',
        },
        {
          key: 'cache-ratio',
          label: t('缓存输入倍率'),
          value: priceData.cacheRatio,
          suffix: 'x',
        },
        {
          key: 'create-cache-ratio',
          label: t('缓存创建倍率'),
          value: priceData.createCacheRatio,
          suffix: 'x',
        },
        {
          key: 'image-ratio',
          label: t('图片输入倍率'),
          value: priceData.imageRatio,
          suffix: 'x',
        },
        {
          key: 'audio-input-ratio',
          label: t('音频输入倍率'),
          value: priceData.audioInputRatio,
          suffix: 'x',
        },
        {
          key: 'audio-output-ratio',
          label: t('音频补全倍率'),
          value: priceData.audioOutputRatio,
          suffix: 'x',
        },
      ].filter(
        (item) =>
          item.value !== null && item.value !== undefined && item.value !== '',
      );
    }

    const unitSuffix = ` / 1${priceData.unitLabel} Tokens`;
    return appendPoints([
      {
        key: 'input',
        label: t('输入价格'),
        value: priceData.inputPrice,
        suffix: unitSuffix,
      },
      {
        key: 'completion',
        label: t('输出价格'),
        value: priceData.completionPrice,
        suffix: unitSuffix,
      },
      {
        key: 'cache',
        label: t('缓存输入价格'),
        value: priceData.cachePrice,
        suffix: unitSuffix,
      },
      {
        key: 'create-cache',
        label: t('缓存创建价格'),
        value: priceData.createCachePrice,
        suffix: unitSuffix,
      },
      {
        key: 'image',
        label: t('图片输入价格'),
        value: priceData.imagePrice,
        suffix: unitSuffix,
      },
      {
        key: 'audio-input',
        label: t('音频输入价格'),
        value: priceData.audioInputPrice,
        suffix: unitSuffix,
      },
      {
        key: 'audio-output',
        label: t('音频输出价格'),
        value: priceData.audioOutputPrice,
        suffix: unitSuffix,
      },
    ]).filter(
      (item) =>
        item.value !== null && item.value !== undefined && item.value !== '',
    );
  }

  return appendPoints(
    [
      {
        key: 'fixed',
        label: t('模型价格'),
        value: priceData.price,
        suffix: ` / ${t('次')}`,
      },
    ].filter(
      (item) =>
        item.value !== null && item.value !== undefined && item.value !== '',
    ),
  );
};

// 格式化动态计费摘要（用于卡片视图，与 formatPriceInfo 风格统一）
export const formatDynamicPriceSummary = (billingExpr, t, groupRatio = 1) => {
  if (!billingExpr)
    return (
      <span style={{ color: 'var(--semi-color-text-1)' }}>{t('动态计费')}</span>
    );

  const { symbol, rate } = getModelPricingCurrencyConfig();

  const gr = groupRatio || 1;
  const exprBody = billingExpr.replace(/^v\d+:/, '');
  const tierMatches = exprBody.match(/tier\(/g) || [];
  const tierCount = tierMatches.length;

  const varCoeffs = {};
  const varRe = new RegExp(BILLING_VAR_REGEX.source, 'g');
  let vm;
  while ((vm = varRe.exec(exprBody)) !== null) {
    if (!(vm[1] in varCoeffs)) varCoeffs[vm[1]] = Number(vm[2]);
  }
  const hasCoeffs = 'p' in varCoeffs || 'c' in varCoeffs;

  const varLabels = BILLING_PRICING_VARS.map((v) => [v.key, v.label]);

  const hasTimeCondition = /\b(?:hour|minute|weekday|month|day)\(/.test(
    exprBody,
  );
  const hasRequestCondition = /\b(?:param|header)\(/.test(exprBody);

  const tags = [];
  if (tierCount > 1) tags.push(`${tierCount}${t('档')}`);
  if (hasTimeCondition) tags.push(t('含时间条件'));
  if (hasRequestCondition) tags.push(t('含请求条件'));

  const unitSuffix = ' / 1M Tokens';
  const lineStyle = { color: 'var(--semi-color-text-1)' };

  return (
    <>
      {hasCoeffs && (
        <>
          {varLabels.map(([key, label]) =>
            key in varCoeffs ? (
              <span key={key} style={lineStyle}>
                {`${t(label)} ${symbol}${formatPriceWithCeiling(varCoeffs[key] * gr * rate)}${unitSuffix}`}
              </span>
            ) : null,
          )}
        </>
      )}
      {(tierCount > 1 || hasTimeCondition || hasRequestCondition) && (
        <span style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          <span
            style={{
              display: 'inline-block',
              padding: '1px 6px',
              borderRadius: 4,
              fontSize: 11,
              background: 'var(--semi-color-warning-light-default)',
              color: 'var(--semi-color-warning)',
            }}
          >
            {t('动态计费')}
          </span>
          {tags.map((tag) => (
            <span
              key={tag}
              style={{
                display: 'inline-block',
                padding: '1px 6px',
                borderRadius: 4,
                fontSize: 11,
                background: 'var(--semi-color-fill-1)',
                color: 'var(--semi-color-text-2)',
              }}
            >
              {tag}
            </span>
          ))}
        </span>
      )}
    </>
  );
};

// 矩阵的纯计算部分拆到 ./videoMatrix，好让手机端直接复用而不是抄一份
// （utils.jsx 会传染桌面依赖、在 mobile 侧被整模块 shim 掉）。
//
// 见文件头 import：formatVideoMatrixSummary 要调 flattenVideoMatrix，理由同上。
export { videoResolutionRank, videoSecondsRank, flattenVideoMatrix };

/**
 * 卡片/表格视图的紧凑摘要：矩阵有多格，列表里放不下，显示价格区间。
 * 详细的逐格价目在详情弹窗里（VideoMatrixBreakdown）。
 */
export const formatVideoMatrixSummary = (priceData, t) => {
  const rows = flattenVideoMatrix(
    priceData?.videoPricing,
    priceData?.usedGroupRatio,
  );
  if (!rows.length) {
    return (
      <span style={{ color: 'var(--semi-color-text-1)' }}>{t('场景计费')}</span>
    );
  }

  const { symbol, rate } = getModelPricingCurrencyConfig();
  const prices = rows.map((r) => r.priceUSD * rate);
  const lo = Math.min(...prices);
  const hi = Math.max(...prices);
  const fmt = (v) => `${symbol}${Number(v.toFixed(2))}`;
  const isToken = priceData.videoPricing.mode === 'token';

  return (
    <>
      <span style={{ color: 'var(--semi-color-text-1)' }}>
        {lo === hi ? fmt(lo) : `${fmt(lo)} ~ ${fmt(hi)}`}
        {isToken ? ` / 1M tokens` : ` / ${t('次')}`}
      </span>
      <span
        style={{
          display: 'inline-block',
          marginLeft: 6,
          padding: '1px 6px',
          borderRadius: 4,
          fontSize: 11,
          background: 'var(--semi-color-fill-1)',
          color: 'var(--semi-color-text-2)',
        }}
      >
        {t('{{count}} 档', { count: rows.length })}
      </span>
    </>
  );
};

// 格式化价格信息（用于卡片视图）
export const formatPriceInfo = (priceData, t, quotaDisplayType = 'USD') => {
  const items = getModelPriceItems(priceData, t, quotaDisplayType);
  return (
    <>
      {items.map((item) => (
        <span key={item.key} style={{ color: 'var(--semi-color-text-1)' }}>
          {item.label} {item.value}
          {item.suffix}
        </span>
      ))}
    </>
  );
};

// -------------------------------
// CardPro 分页配置函数
// 用于创建 CardPro 的 paginationArea 配置
export const createCardProPagination = ({
  currentPage,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
  isMobile = false,
  pageSizeOpts = [10, 20, 50, 100],
  showSizeChanger = true,
  t = (key) => key,
}) => {
  if (!total || total <= 0) return null;

  const start = (currentPage - 1) * pageSize + 1;
  const end = Math.min(currentPage * pageSize, total);
  const totalText = `${t('显示第')} ${start} ${t('条 - 第')} ${end} ${t('条，共')} ${total} ${t('条')}`;

  return (
    <>
      {/* 桌面端左侧总数信息 */}
      {!isMobile && (
        <span
          className='text-sm select-none'
          style={{ color: 'var(--semi-color-text-2)' }}
        >
          {totalText}
        </span>
      )}

      {/* 右侧分页控件 */}
      <Pagination
        currentPage={currentPage}
        pageSize={pageSize}
        total={total}
        pageSizeOpts={pageSizeOpts}
        showSizeChanger={showSizeChanger}
        onPageSizeChange={onPageSizeChange}
        onPageChange={onPageChange}
        size={isMobile ? 'small' : 'default'}
        showQuickJumper={isMobile}
        showTotal
      />
    </>
  );
};

// 模型定价筛选条件默认值
const DEFAULT_PRICING_FILTERS = {
  search: '',
  showWithRecharge: false,
  currency: 'USD',
  showRatio: false,
  viewMode: 'card',
  tokenUnit: 'M',
  filterGroup: 'all',
  filterQuotaType: 'all',
  filterEndpointType: 'all',
  filterVendor: 'all',
  filterTag: 'all',
  filterCapability: 'all',
  currentPage: 1,
};

// 重置模型定价筛选条件
export const resetPricingFilters = ({
  handleChange,
  setShowWithRecharge,
  setCurrency,
  setShowRatio,
  setViewMode,
  setFilterGroup,
  setFilterQuotaType,
  setFilterEndpointType,
  setFilterVendor,
  setFilterTag,
  setFilterCapability,
  setCurrentPage,
  setTokenUnit,
}) => {
  handleChange?.(DEFAULT_PRICING_FILTERS.search);
  setShowWithRecharge?.(DEFAULT_PRICING_FILTERS.showWithRecharge);
  setCurrency?.(DEFAULT_PRICING_FILTERS.currency);
  setShowRatio?.(DEFAULT_PRICING_FILTERS.showRatio);
  setViewMode?.(DEFAULT_PRICING_FILTERS.viewMode);
  setTokenUnit?.(DEFAULT_PRICING_FILTERS.tokenUnit);
  setFilterGroup?.(DEFAULT_PRICING_FILTERS.filterGroup);
  setFilterQuotaType?.(DEFAULT_PRICING_FILTERS.filterQuotaType);
  setFilterEndpointType?.(DEFAULT_PRICING_FILTERS.filterEndpointType);
  setFilterVendor?.(DEFAULT_PRICING_FILTERS.filterVendor);
  setFilterTag?.(DEFAULT_PRICING_FILTERS.filterTag);
  setFilterCapability?.(DEFAULT_PRICING_FILTERS.filterCapability);
  setCurrentPage?.(DEFAULT_PRICING_FILTERS.currentPage);
};
