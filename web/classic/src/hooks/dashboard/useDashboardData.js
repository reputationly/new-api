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

import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  API,
  isAdmin,
  showError,
  showWarning,
  timestamp2string,
} from '../../helpers';
import {
  getDefaultTime,
  getInitialTimestamp,
  parseDashboardTimestamp,
  validateDashboardRange,
  getPresetRange,
  getGranularityForRange,
  formatRangeSummary,
} from '../../helpers/dashboard';
import { TIME_OPTIONS } from '../../constants/dashboard.constants';
import { useIsMobile } from '../common/useIsMobile';
import { useMinimumLoadingTime } from '../common/useMinimumLoadingTime';

export const useDashboardData = (userState, userDispatch, statusState) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isMobile = useIsMobile();
  const initialized = useRef(false);

  // ========== 基础状态 ==========
  const [loading, setLoading] = useState(false);
  const [greetingVisible, setGreetingVisible] = useState(false);
  const [searchModalVisible, setSearchModalVisible] = useState(false);
  const showLoading = useMinimumLoadingTime(loading);

  // ========== 输入状态 ==========
  const [inputs, setInputs] = useState({
    username: '',
    token_name: '',
    model_name: '',
    start_timestamp: getInitialTimestamp(),
    end_timestamp: timestamp2string(new Date().getTime() / 1000 + 3600),
    channel: '',
    data_export_default_time: '',
  });

  const [dataExportDefaultTime, setDataExportDefaultTime] =
    useState(getDefaultTime());

  // ========== 数据状态 ==========
  const [quotaData, setQuotaData] = useState([]);
  // 分组消耗（线路维度）单独存：它走汇总表接口，且只在切到对应 tab 时才拉。
  const [groupQuotaData, setGroupQuotaData] = useState([]);
  const [groupQuotaLoading, setGroupQuotaLoading] = useState(false);
  const [activePreset, setActivePreset] = useState('');
  // 已确认的查询版本号：搜索确认/刷新时自增。分组消耗表监听它而不是监听 inputs，
  // 否则用户在弹窗里改时间（还没点确定）就会触发请求。
  const [dataQueryVersion, setDataQueryVersion] = useState(0);
  const [consumeQuota, setConsumeQuota] = useState(0);
  const [consumeTokens, setConsumeTokens] = useState(0);
  const [times, setTimes] = useState(0);
  const [pieData, setPieData] = useState([{ type: 'null', value: '0' }]);
  const [lineData, setLineData] = useState([]);
  const [modelColors, setModelColors] = useState({});

  // ========== 图表状态 ==========
  const [activeChartTab, setActiveChartTab] = useState('1');

  // ========== 趋势数据 ==========
  const [trendData, setTrendData] = useState({
    balance: [],
    usedQuota: [],
    requestCount: [],
    times: [],
    consumeQuota: [],
    tokens: [],
    rpm: [],
    tpm: [],
  });

  // ========== Uptime 数据 ==========
  const [uptimeData, setUptimeData] = useState([]);
  const [uptimeLoading, setUptimeLoading] = useState(false);
  const [activeUptimeTab, setActiveUptimeTab] = useState('');

  // ========== 常量 ==========
  const now = new Date();
  const isAdminUser = isAdmin();

  // ========== Panel enable flags ==========
  const apiInfoEnabled = statusState?.status?.api_info_enabled ?? true;
  const announcementsEnabled =
    statusState?.status?.announcements_enabled ?? true;
  const faqEnabled = statusState?.status?.faq_enabled ?? true;
  const uptimeEnabled = statusState?.status?.uptime_kuma_enabled ?? true;

  const hasApiInfoPanel = apiInfoEnabled;
  const hasInfoPanels = announcementsEnabled || faqEnabled || uptimeEnabled;

  // ========== Memoized Values ==========
  const timeOptions = useMemo(
    () =>
      TIME_OPTIONS.map((option) => ({
        ...option,
        label: t(option.label),
      })),
    [t],
  );

  const performanceMetrics = useMemo(() => {
    const { start_timestamp, end_timestamp } = inputs;
    const timeDiff =
      (Date.parse(end_timestamp) - Date.parse(start_timestamp)) / 60000;
    const avgRPM = isNaN(times / timeDiff)
      ? '0'
      : (times / timeDiff).toFixed(3);
    const avgTPM = isNaN(consumeTokens / timeDiff)
      ? '0'
      : (consumeTokens / timeDiff).toFixed(3);

    return { avgRPM, avgTPM, timeDiff };
  }, [times, consumeTokens, inputs.start_timestamp, inputs.end_timestamp]);

  const getGreeting = useMemo(() => {
    const hours = new Date().getHours();
    let greeting = '';

    if (hours >= 5 && hours < 12) {
      greeting = t('早上好');
    } else if (hours >= 12 && hours < 14) {
      greeting = t('中午好');
    } else if (hours >= 14 && hours < 18) {
      greeting = t('下午好');
    } else {
      greeting = t('晚上好');
    }

    const username = userState?.user?.username || '';
    return `👋${greeting}，${username}`;
  }, [t, userState?.user?.username]);

  // ========== 回调函数 ==========
  const handleInputChange = useCallback((value, name) => {
    if (name === 'data_export_default_time') {
      // 刻意不再写 localStorage：这个键同时被 /api/status 和站点设置写入，
      // 看板再写一次会让「用户选择」被下一次状态刷新覆盖，表现为时灵时不灵。
      // 粒度初值统一由站点设置「数据看板默认时间粒度」决定。
      setDataExportDefaultTime(value);
      return;
    }
    if (name === 'start_timestamp' || name === 'end_timestamp') {
      // 手改时间就不再是任何预设，清掉高亮。
      setActivePreset('');
    }
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  }, []);

  // 应用快捷范围：同时填起止时间并按跨度切粒度。
  const applyPreset = useCallback((key) => {
    // 「自定义」不清空时间，只取消预设高亮——用户接着手改时间框。
    if (key === 'custom') {
      setActivePreset('');
      return;
    }
    const range = getPresetRange(key);
    if (!range) return;
    setActivePreset(key);
    setInputs((inputs) => ({
      ...inputs,
      start_timestamp: range.start_timestamp,
      end_timestamp: range.end_timestamp,
    }));
    const granularity =
      range.granularity ||
      getGranularityForRange(
        parseDashboardTimestamp(range.start_timestamp),
        parseDashboardTimestamp(range.end_timestamp),
      );
    setDataExportDefaultTime(granularity);
  }, []);

  const showSearchModal = useCallback(() => {
    setSearchModalVisible(true);
  }, []);

  const handleCloseModal = useCallback(() => {
    setSearchModalVisible(false);
  }, []);

  // ========== API 调用函数 ==========
  const loadQuotaData = useCallback(async () => {
    setLoading(true);
    try {
      let url = '';
      const { start_timestamp, end_timestamp, username } = inputs;
      let localStartTimestamp = parseDashboardTimestamp(start_timestamp);
      let localEndTimestamp = parseDashboardTimestamp(end_timestamp);

      if (isAdminUser) {
        url = `/api/data/?username=${username}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}&default_time=${dataExportDefaultTime}`;
      } else {
        url = `/api/data/self/?start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}&default_time=${dataExportDefaultTime}`;
      }

      const res = await API.get(url);
      const { success, message, data } = res.data;
      if (success) {
        setQuotaData(data);
        if (data.length === 0) {
          data.push({
            count: 0,
            model_name: '无数据',
            quota: 0,
            created_at: now.getTime() / 1000,
          });
        }
        data.sort((a, b) => a.created_at - b.created_at);
        return data;
      } else {
        showError(message);
        return [];
      }
    } finally {
      setLoading(false);
    }
  }, [inputs, dataExportDefaultTime, isAdminUser, now]);

  const loadUptimeData = useCallback(async () => {
    setUptimeLoading(true);
    try {
      const res = await API.get('/api/uptime/status');
      const { success, message, data } = res.data;
      if (success) {
        setUptimeData(data || []);
        if (data && data.length > 0 && !activeUptimeTab) {
          setActiveUptimeTab(data[0].categoryName);
        }
      } else {
        showError(message);
      }
    } catch (err) {
      console.error(err);
    } finally {
      setUptimeLoading(false);
    }
  }, [activeUptimeTab]);

  const loadUserQuotaData = useCallback(async () => {
    if (!isAdminUser) return [];
    try {
      const { start_timestamp, end_timestamp } = inputs;
      const localStartTimestamp = parseDashboardTimestamp(start_timestamp);
      const localEndTimestamp = parseDashboardTimestamp(end_timestamp);
      const url = `/api/data/users?start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}`;
      const res = await API.get(url);
      const { success, message, data } = res.data;
      if (success) {
        return data || [];
      } else {
        showError(message);
        return [];
      }
    } catch (err) {
      console.error(err);
      return [];
    }
  }, [inputs, isAdminUser]);

  // 分组消耗：数据源是 logs 的小时汇总表，成本与窗口长度无关，但仍是本页最重的一次查询。
  // 刻意不在页面初始化时拉，由调用方在「切到分组 tab / 确认搜索 / 刷新」时才触发。
  //
  // 起止时间显式传参而不是读 inputs：这样函数身份稳定，调用方可以把它放进 effect 依赖，
  // 而不会因为用户在弹窗里拖动时间就每改一次发一个请求。
  const loadGroupQuotaData = useCallback(
    async (startTimestamp, endTimestamp, username) => {
      if (!isAdminUser) {
        setGroupQuotaData([]);
        return [];
      }
      const error = validateDashboardRange(startTimestamp, endTimestamp);
      if (error) {
        setGroupQuotaData([]);
        return [];
      }
      const start = parseDashboardTimestamp(startTimestamp);
      const end = parseDashboardTimestamp(endTimestamp);
      setGroupQuotaLoading(true);
      try {
        const res = await API.get(
          `/api/data/groups?start_timestamp=${start}&end_timestamp=${end}&username=${encodeURIComponent(username || '')}`,
        );
        const { success, message, data } = res.data;
        if (success) {
          setGroupQuotaData(data || []);
          return data || [];
        }
        setGroupQuotaData([]);
        showError(message);
        return [];
      } catch (err) {
        console.error(err);
        setGroupQuotaData([]);
        return [];
      } finally {
        setGroupQuotaLoading(false);
      }
    },
    [isAdminUser],
  );

  const getUserData = useCallback(async () => {
    let res = await API.get(`/api/user/self`);
    const { success, message, data } = res.data;
    if (success) {
      userDispatch({ type: 'login', payload: data });
    } else {
      showError(message);
    }
  }, [userDispatch]);

  const refresh = useCallback(async () => {
    const data = await loadQuotaData();
    await loadUptimeData();
    setDataQueryVersion((version) => version + 1);
    return data;
  }, [loadQuotaData, loadUptimeData]);

  const handleSearchConfirm = useCallback(
    async (updateChartDataCallback) => {
      // 时间条件不合法时不发请求、不关弹窗：发出去只会得到空结果或（起始为空时）
      // 一次全表聚合，用户看到的是「没数据」而不是「条件错了」。
      const error = validateDashboardRange(
        inputs.start_timestamp,
        inputs.end_timestamp,
      );
      if (error) {
        showWarning(error);
        return;
      }
      const data = await refresh();
      if (data && data.length > 0 && updateChartDataCallback) {
        updateChartDataCallback(data);
      }
      setSearchModalVisible(false);
    },
    [refresh, inputs.start_timestamp, inputs.end_timestamp],
  );

  const rangeSummary = useMemo(
    () =>
      formatRangeSummary(
        inputs.start_timestamp,
        inputs.end_timestamp,
        dataExportDefaultTime,
      ),
    [inputs.start_timestamp, inputs.end_timestamp, dataExportDefaultTime],
  );

  // ========== Effects ==========
  useEffect(() => {
    const timer = setTimeout(() => {
      setGreetingVisible(true);
    }, 100);
    return () => clearTimeout(timer);
  }, []);

  useEffect(() => {
    if (!initialized.current) {
      getUserData();
      initialized.current = true;
    }
  }, [getUserData]);

  return {
    // 基础状态
    loading: showLoading,
    greetingVisible,
    searchModalVisible,

    // 输入状态
    inputs,
    dataExportDefaultTime,
    activePreset,
    applyPreset,
    rangeSummary,
    dataQueryVersion,

    // 数据状态
    quotaData,
    groupQuotaData,
    groupQuotaLoading,
    loadGroupQuotaData,
    consumeQuota,
    setConsumeQuota,
    consumeTokens,
    setConsumeTokens,
    times,
    setTimes,
    pieData,
    setPieData,
    lineData,
    setLineData,
    modelColors,
    setModelColors,

    // 图表状态
    activeChartTab,
    setActiveChartTab,

    // 趋势数据
    trendData,
    setTrendData,

    // Uptime 数据
    uptimeData,
    uptimeLoading,
    activeUptimeTab,
    setActiveUptimeTab,

    // 计算值
    timeOptions,
    performanceMetrics,
    getGreeting,
    isAdminUser,
    hasApiInfoPanel,
    hasInfoPanels,
    apiInfoEnabled,
    announcementsEnabled,
    faqEnabled,
    uptimeEnabled,

    // 函数
    handleInputChange,
    showSearchModal,
    handleCloseModal,
    loadQuotaData,
    loadUserQuotaData,
    loadUptimeData,
    getUserData,
    refresh,
    handleSearchConfirm,

    // 导航和翻译
    navigate,
    t,
    isMobile,
  };
};
