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

import { useState, useCallback, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import {
  DEFAULT_MESSAGES,
  getDefaultMessages,
  DEFAULT_CONFIG,
  DEBUG_TABS,
  MESSAGE_STATUS,
} from '../../constants/playground.constants';
import {
  loadConfig,
  saveConfig,
  loadMessages,
  saveMessages,
  clearMessages,
} from '../../components/playground/configStorage';
import { processIncompleteThinkTags } from '../../helpers';

// 把末条卡在 LOADING/INCOMPLETE 的消息(如半程刷新)修成 COMPLETE。无需修复时返回原数组
// 引用(调用方据此判断是否要回存)。mount 修复 effect 与异步 hydrate 共用,避免异步加载
// 出的卡死消息漏修。
const repairIncompleteLast = (messages) => {
  if (!Array.isArray(messages) || messages.length === 0) return messages;
  const lastMsg = messages[messages.length - 1];
  if (
    lastMsg.status !== MESSAGE_STATUS.LOADING &&
    lastMsg.status !== MESSAGE_STATUS.INCOMPLETE
  ) {
    return messages;
  }
  const processed = processIncompleteThinkTags(
    lastMsg.content || '',
    lastMsg.reasoningContent || '',
  );
  const fixedLastMsg = {
    ...lastMsg,
    status: MESSAGE_STATUS.COMPLETE,
    content: processed.content,
    reasoningContent: processed.reasoningContent || null,
    isThinkingComplete: true,
  };
  return [...messages.slice(0, -1), fixedLastMsg];
};

export const usePlaygroundState = () => {
  const { t } = useTranslation();

  // 使用惰性初始化，确保只在组件首次挂载时加载配置
  const [savedConfig] = useState(() => loadConfig());
  // 消息改存 IndexedDB(异步),初始为默认,mount 后 hydrate。hasStoredMessagesRef 标记
  // 是否已从存储恢复出真实消息——供语言 effect 判断该不该用默认消息覆盖。
  // initialDefaultRef 持有"当前默认消息对象":hydrate 只在 message 仍严格等于它(用户
  // 尚未发消息/编辑)时才覆盖,避免异步 loadMessages 迟到时把用户刚发的消息冲掉。
  const hasStoredMessagesRef = useRef(false);
  const initialDefaultRef = useRef(null);

  // 基础配置状态
  const [inputs, setInputs] = useState(
    savedConfig.inputs || DEFAULT_CONFIG.inputs,
  );
  const [parameterEnabled, setParameterEnabled] = useState(
    savedConfig.parameterEnabled || DEFAULT_CONFIG.parameterEnabled,
  );
  const [showDebugPanel, setShowDebugPanel] = useState(
    savedConfig.showDebugPanel || DEFAULT_CONFIG.showDebugPanel,
  );
  const [customRequestMode, setCustomRequestMode] = useState(
    savedConfig.customRequestMode || DEFAULT_CONFIG.customRequestMode,
  );
  const [customRequestBody, setCustomRequestBody] = useState(
    savedConfig.customRequestBody || DEFAULT_CONFIG.customRequestBody,
  );

  // UI状态
  const [showSettings, setShowSettings] = useState(false);
  const [models, setModels] = useState([]);
  const [modelEndpointTypes, setModelEndpointTypes] = useState(() => new Map());
  const [groups, setGroups] = useState([]);
  const [status, setStatus] = useState({});

  // 消息相关状态 - 先用默认消息,mount 后从 IDB 异步 hydrate 覆盖
  const [message, setMessage] = useState(() => {
    const def = getDefaultMessages(t);
    initialDefaultRef.current = def;
    return def;
  });

  // mount 后从 IDB 加载历史消息(含旧 localStorage 一次性迁移)。聊天是单文档,无多会话
  // 竞态,直接覆盖即可;canceled 兜 StrictMode 双挂载。
  useEffect(() => {
    let canceled = false;
    loadMessages().then((loaded) => {
      if (canceled || !loaded) return;
      // 旧的中文默认消息:清掉,不恢复
      if (
        loaded.length === 2 &&
        loaded[0].id === '2' &&
        loaded[1].id === '3'
      ) {
        const hasOldChinese =
          loaded[0].content === '你好' ||
          loaded[1].content === '你好，请问有什么可以帮助您的吗？' ||
          loaded[1].content === '你好！很高兴见到你。有什么我可以帮助你的吗？';
        if (hasOldChinese) {
          clearMessages();
          return;
        }
      }
      hasStoredMessagesRef.current = true;
      // 修复加载出的末条卡死消息(半程刷新):mount 修复 effect 只对默认消息跑过一次,
      // 看不到异步加载的这批,否则会永远卡在 LOADING/INCOMPLETE。
      const repaired = repairIncompleteLast(loaded);
      // 只在用户尚未改动消息(仍严格等于初始默认对象)时覆盖:若用户已发消息/编辑,
      // current 已是新数组 → 保留用户的,不被迟到的历史冲掉。
      let applied = false;
      setMessage((current) => {
        if (current === initialDefaultRef.current) {
          applied = true;
          return repaired;
        }
        return current;
      });
      if (applied && repaired !== loaded) {
        setTimeout(() => saveMessagesImmediately(repaired), 0);
      }
    });
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 当语言改变时，若尚未从存储恢复出真实消息(仍是默认),才更新默认消息文案。
  // 同步更新 initialDefaultRef,让 hydrate 的"仍是默认"判定对新语言默认仍成立。
  useEffect(() => {
    if (!hasStoredMessagesRef.current) {
      const def = getDefaultMessages(t);
      initialDefaultRef.current = def;
      setMessage(def);
    }
  }, [t]); // 当语言改变时

  // 调试状态
  const [debugData, setDebugData] = useState({
    request: null,
    headers: null,
    response: null,
    timestamp: null,
    previewRequest: null,
    previewTimestamp: null,
  });
  const [activeDebugTab, setActiveDebugTab] = useState(DEBUG_TABS.PREVIEW);
  const [previewPayload, setPreviewPayload] = useState(null);

  // 编辑状态
  const [editingMessageId, setEditingMessageId] = useState(null);
  const [editValue, setEditValue] = useState('');

  // Quota exceeded state
  const [quotaExceeded, setQuotaExceeded] = useState(false);
  const quotaToastShownRef = useRef(false);

  const resetQuotaExceeded = useCallback(() => {
    quotaToastShownRef.current = false;
    setQuotaExceeded(false);
  }, []);

  // Refs
  const sseSourceRef = useRef(null);
  const chatRef = useRef(null);
  const saveConfigTimeoutRef = useRef(null);
  const saveMessagesTimeoutRef = useRef(null);

  // 配置更新函数
  const handleInputChange = useCallback((name, value) => {
    setInputs((prev) => ({ ...prev, [name]: value }));
  }, []);

  const handleParameterToggle = useCallback((paramName) => {
    setParameterEnabled((prev) => ({
      ...prev,
      [paramName]: !prev[paramName],
    }));
  }, []);

  // 消息保存函数 - 改为立即保存，可以接受参数
  const saveMessagesImmediately = useCallback(
    (messagesToSave) => {
      const result = saveMessages(messagesToSave || message);
      if (result === 'quota' && !quotaToastShownRef.current) {
        quotaToastShownRef.current = true;
        setQuotaExceeded(true);
      }
    },
    [message],
  );

  // 配置保存
  const debouncedSaveConfig = useCallback(() => {
    if (saveConfigTimeoutRef.current) {
      clearTimeout(saveConfigTimeoutRef.current);
    }

    saveConfigTimeoutRef.current = setTimeout(() => {
      const configToSave = {
        inputs,
        parameterEnabled,
        showDebugPanel,
        customRequestMode,
        customRequestBody,
      };
      saveConfig(configToSave);
    }, 1000);
  }, [
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,
  ]);

  // 配置导入/重置
  const handleConfigImport = useCallback((importedConfig) => {
    if (importedConfig.inputs) {
      const parsedMaxTokens = parseInt(importedConfig.inputs.max_tokens, 10);
      setInputs((prev) => ({
        ...prev,
        ...importedConfig.inputs,
        max_tokens: Number.isNaN(parsedMaxTokens)
          ? importedConfig.inputs.max_tokens
          : parsedMaxTokens,
      }));
    }
    if (importedConfig.parameterEnabled) {
      setParameterEnabled((prev) => ({
        ...prev,
        ...importedConfig.parameterEnabled,
      }));
    }
    if (typeof importedConfig.showDebugPanel === 'boolean') {
      setShowDebugPanel(importedConfig.showDebugPanel);
    }
    if (importedConfig.customRequestMode) {
      setCustomRequestMode(importedConfig.customRequestMode);
    }
    if (importedConfig.customRequestBody) {
      setCustomRequestBody(importedConfig.customRequestBody);
    }
    // 如果导入的配置包含消息，也恢复消息
    if (importedConfig.messages && Array.isArray(importedConfig.messages)) {
      setMessage(importedConfig.messages);
    }
  }, []);

  const handleConfigReset = useCallback((options = {}) => {
    const { resetMessages = false } = options;

    setInputs(DEFAULT_CONFIG.inputs);
    setParameterEnabled(DEFAULT_CONFIG.parameterEnabled);
    setShowDebugPanel(DEFAULT_CONFIG.showDebugPanel);
    setCustomRequestMode(DEFAULT_CONFIG.customRequestMode);
    setCustomRequestBody(DEFAULT_CONFIG.customRequestBody);

    // 只有在明确指定时才重置消息
    if (resetMessages) {
      setMessage([]);
      setTimeout(() => {
        setMessage(getDefaultMessages(t));
      }, 0);
    }
  }, []);

  // 清理定时器
  useEffect(() => {
    return () => {
      if (saveConfigTimeoutRef.current) {
        clearTimeout(saveConfigTimeoutRef.current);
      }
    };
  }, []);

  // 页面首次加载时，若最后一条消息仍处于 LOADING/INCOMPLETE 状态，自动修复。
  // 消息现在异步 hydrate,mount 时通常仍是默认消息(无需修复);真正的修复发生在上面的
  // hydrate .then 里。此处保留以覆盖任何同步已有非默认消息的情形。
  useEffect(() => {
    const repaired = repairIncompleteLast(message);
    if (repaired !== message) {
      setMessage(repaired);
      setTimeout(() => saveMessagesImmediately(repaired), 0);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return {
    // 配置状态
    quotaExceeded,
    resetQuotaExceeded,
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,

    // UI状态
    showSettings,
    models,
    modelEndpointTypes,
    groups,
    status,

    // 消息状态
    message,

    // 调试状态
    debugData,
    activeDebugTab,
    previewPayload,

    // 编辑状态
    editingMessageId,
    editValue,

    // Refs
    sseSourceRef,
    chatRef,
    saveConfigTimeoutRef,

    // 更新函数
    setInputs,
    setParameterEnabled,
    setShowDebugPanel,
    setCustomRequestMode,
    setCustomRequestBody,
    setShowSettings,
    setModels,
    setModelEndpointTypes,
    setGroups,
    setStatus,
    setMessage,
    setDebugData,
    setActiveDebugTab,
    setPreviewPayload,
    setEditingMessageId,
    setEditValue,

    // 处理函数
    handleInputChange,
    handleParameterToggle,
    debouncedSaveConfig,
    saveMessagesImmediately,
    handleConfigImport,
    handleConfigReset,
  };
};
