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

import React from 'react';
import { Progress, Divider, Empty } from '@douyinfe/semi-ui';
import {
  IllustrationConstruction,
  IllustrationConstructionDark,
} from '@douyinfe/semi-illustrations';
import {
  timestamp2string,
  timestamp2string1,
  isDataCrossYear,
  copy,
  showSuccess,
} from './utils';
import {
  STORAGE_KEYS,
  DEFAULT_TIME_INTERVALS,
  DEFAULTS,
  ILLUSTRATION_SIZE,
} from '../constants/dashboard.constants';

// ========== 时间相关工具函数 ==========
export const getDefaultTime = () => {
  return localStorage.getItem(STORAGE_KEYS.DATA_EXPORT_DEFAULT_TIME) || 'hour';
};

export const getTimeInterval = (timeType, isSeconds = false) => {
  const intervals =
    DEFAULT_TIME_INTERVALS[timeType] || DEFAULT_TIME_INTERVALS.hour;
  return isSeconds ? intervals.seconds : intervals.minutes;
};

export const getInitialTimestamp = () => {
  const defaultTime = getDefaultTime();
  const now = new Date().getTime() / 1000;

  switch (defaultTime) {
    case 'hour':
      return timestamp2string(now - 86400);
    case 'week':
      return timestamp2string(now - 86400 * 30);
    default:
      return timestamp2string(now - 86400 * 7);
  }
};

// ========== 数据看板的时间范围 ==========

// 与后端 dataDashboardMaxRangeSeconds 一致（controller/usedata.go）。
// 弹窗与接口必须同一个上限，否则会出现「能选、提交后报错」。
export const DATA_DASHBOARD_MAX_RANGE_SECONDS = 90 * 86400;

// Date.parse 对 'YYYY-MM-DD HH:mm:ss' 的解析在部分浏览器（Safari）上会返回 NaN，
// 一旦 NaN 传到后端就被解析成 0：起始为 0 是全表扫描，结束为 0 是恒空结果。
// 统一走这个函数兜底，别在调用点各写一份。
export const parseDashboardTimestamp = (value) => {
  if (!value) return NaN;
  const parsed = Date.parse(value);
  if (Number.isFinite(parsed)) return parsed / 1000;
  return Date.parse(String(value).replace(/-/g, '/')) / 1000;
};

// 快捷范围：决定「查哪一段」；粒度只决定「横轴怎么分桶」，两者互不替代。
export const TIME_PRESETS = [
  { key: 'today', label: '今天', granularity: 'hour' },
  { key: 'yesterday', label: '昨天', granularity: 'hour' },
  { key: 'last24h', label: '近24小时', granularity: 'hour' },
  { key: 'last7d', label: '近7天', granularity: 'day' },
  { key: 'last30d', label: '近30天', granularity: 'day' },
  { key: 'lastMonth', label: '上个月', granularity: 'day' },
];

const startOfDay = (date) =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate(), 0, 0, 0);
const endOfDay = (date) =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate(), 23, 59, 59);

// getPresetRange 返回该预设对应的起止时间（与 DatePicker 相同的字符串格式）与建议粒度。
//
// 「上个月」按日历月计算（1 月要跨年、2 月 28 天、10 月 31 天），不是「最近 30 天」——
// 后者在月初会把本月头几天算进去，运营对不上账。
export const getPresetRange = (key) => {
  const now = new Date();
  const toStr = (date) => timestamp2string(date.getTime() / 1000);
  switch (key) {
    case 'today':
      return {
        start_timestamp: toStr(startOfDay(now)),
        end_timestamp: toStr(now),
        granularity: 'hour',
      };
    case 'yesterday': {
      const yesterday = new Date(now.getTime() - 86400 * 1000);
      return {
        start_timestamp: toStr(startOfDay(yesterday)),
        end_timestamp: toStr(endOfDay(yesterday)),
        granularity: 'hour',
      };
    }
    case 'last24h':
      return {
        start_timestamp: toStr(new Date(now.getTime() - 86400 * 1000)),
        end_timestamp: toStr(now),
        granularity: 'hour',
      };
    case 'last7d':
      return {
        start_timestamp: toStr(new Date(now.getTime() - 7 * 86400 * 1000)),
        end_timestamp: toStr(now),
        granularity: 'day',
      };
    case 'last30d':
      return {
        start_timestamp: toStr(new Date(now.getTime() - 30 * 86400 * 1000)),
        end_timestamp: toStr(now),
        granularity: 'day',
      };
    case 'lastMonth': {
      const firstOfThisMonth = new Date(now.getFullYear(), now.getMonth(), 1);
      // 1 月时 getMonth()-1 = -1，Date 会正确回退到上一年 12 月。
      const lastMonthStart = new Date(now.getFullYear(), now.getMonth() - 1, 1);
      const lastMonthEnd = new Date(firstOfThisMonth.getTime() - 1000);
      return {
        start_timestamp: toStr(startOfDay(lastMonthStart)),
        end_timestamp: toStr(endOfDay(lastMonthEnd)),
        granularity: 'day',
      };
    }
    default:
      return null;
  }
};

// 按跨度给出合适粒度：48 小时以内按小时、31 天以内按天、更长按周。
export const getGranularityForRange = (startSeconds, endSeconds) => {
  const span = endSeconds - startSeconds;
  if (!Number.isFinite(span) || span <= 0) return 'hour';
  if (span <= 48 * 3600) return 'hour';
  if (span <= 31 * 86400) return 'day';
  return 'week';
};

// 校验搜索弹窗的时间条件，返回错误文案（空串表示通过）。
export const validateDashboardRange = (startTimestamp, endTimestamp) => {
  const start = parseDashboardTimestamp(startTimestamp);
  const end = parseDashboardTimestamp(endTimestamp);
  if (!Number.isFinite(start) || !Number.isFinite(end)) {
    return '请选择起始时间和结束时间';
  }
  if (end <= start) {
    return '结束时间必须晚于起始时间';
  }
  if (end - start > DATA_DASHBOARD_MAX_RANGE_SECONDS) {
    return '时间跨度不能超过 90 天';
  }
  return '';
};

// 页面顶部展示的当前范围摘要，如「近7天 · 11-07 18:36 → 11-14 19:36 · 按天」。
export const formatRangeSummary = (
  startTimestamp,
  endTimestamp,
  granularity,
) => {
  const start = parseDashboardTimestamp(startTimestamp);
  const end = parseDashboardTimestamp(endTimestamp);
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '';
  const fmt = (seconds) => timestamp2string(seconds).slice(5, 16);
  const span = end - start;
  let spanText = '';
  if (span < 3600) {
    spanText = `${Math.max(1, Math.round(span / 60))} 分钟`;
  } else if (span < 86400) {
    spanText = `${Math.round(span / 3600)} 小时`;
  } else {
    spanText = `${Math.round(span / 86400)} 天`;
  }
  const granularityText =
    {
      hour: '按小时',
      day: '按天',
      week: '按周',
    }[granularity] || '按天';
  return `${spanText} · ${fmt(start)} → ${fmt(end)} · ${granularityText}`;
};

// ========== 数据处理工具函数 ==========
export const updateMapValue = (map, key, value) => {
  if (!map.has(key)) {
    map.set(key, 0);
  }
  map.set(key, map.get(key) + value);
};

export const initializeMaps = (key, ...maps) => {
  maps.forEach((map) => {
    if (!map.has(key)) {
      map.set(key, 0);
    }
  });
};

// ========== 图表相关工具函数 ==========
export const updateChartSpec = (
  setterFunc,
  newData,
  subtitle,
  newColors,
  dataId,
) => {
  setterFunc((prev) => ({
    ...prev,
    data: [{ id: dataId, values: newData }],
    title: {
      ...prev.title,
      subtext: subtitle,
    },
    color: {
      specified: newColors,
    },
  }));
};

export const getTrendSpec = (data, color) => ({
  type: 'line',
  data: [{ id: 'trend', values: data.map((val, idx) => ({ x: idx, y: val })) }],
  xField: 'x',
  yField: 'y',
  height: 40,
  width: 100,
  axes: [
    {
      orient: 'bottom',
      visible: false,
    },
    {
      orient: 'left',
      visible: false,
    },
  ],
  padding: 0,
  autoFit: false,
  legends: { visible: false },
  tooltip: { visible: false },
  crosshair: { visible: false },
  line: {
    style: {
      stroke: color,
      lineWidth: 2,
    },
  },
  point: {
    visible: false,
  },
  background: {
    fill: 'transparent',
  },
});

// ========== UI 工具函数 ==========
export const createSectionTitle = (Icon, text) => (
  <div className='flex items-center gap-2'>
    <Icon size={16} />
    {text}
  </div>
);

export const createFormField = (Component, props, FORM_FIELD_PROPS) => (
  <Component {...FORM_FIELD_PROPS} {...props} />
);

// ========== 操作处理函数 ==========
export const handleCopyUrl = async (url, t) => {
  if (await copy(url)) {
    showSuccess(t('复制成功'));
  }
};

export const handleSpeedTest = (apiUrl) => {
  const encodedUrl = encodeURIComponent(apiUrl);
  const speedTestUrl = `https://www.tcptest.cn/http/${encodedUrl}`;
  window.open(speedTestUrl, '_blank', 'noopener,noreferrer');
};

// ========== 状态映射函数 ==========
export const getUptimeStatusColor = (status, uptimeStatusMap) =>
  uptimeStatusMap[status]?.color || '#8b9aa7';

export const getUptimeStatusText = (status, uptimeStatusMap, t) =>
  uptimeStatusMap[status]?.text || t('未知');

// ========== 监控列表渲染函数 ==========
export const renderMonitorList = (
  monitors,
  getUptimeStatusColor,
  getUptimeStatusText,
  t,
) => {
  if (!monitors || monitors.length === 0) {
    return (
      <div className='flex justify-center items-center py-4'>
        <Empty
          image={<IllustrationConstruction style={ILLUSTRATION_SIZE} />}
          darkModeImage={
            <IllustrationConstructionDark style={ILLUSTRATION_SIZE} />
          }
          title={t('暂无监控数据')}
        />
      </div>
    );
  }

  const grouped = {};
  monitors.forEach((m) => {
    const g = m.group || '';
    if (!grouped[g]) grouped[g] = [];
    grouped[g].push(m);
  });

  const renderItem = (monitor, idx) => (
    <div key={idx} className='p-2 hover:bg-white rounded-lg transition-colors'>
      <div className='flex items-center justify-between mb-1'>
        <div className='flex items-center gap-2'>
          <div
            className='w-2 h-2 rounded-full flex-shrink-0'
            style={{ backgroundColor: getUptimeStatusColor(monitor.status) }}
          />
          <span className='text-sm font-medium text-gray-900'>
            {monitor.name}
          </span>
        </div>
        <span className='text-xs text-gray-500'>
          {((monitor.uptime || 0) * 100).toFixed(2)}%
        </span>
      </div>
      <div className='flex items-center gap-2'>
        <span className='text-xs text-gray-500'>
          {getUptimeStatusText(monitor.status)}
        </span>
        <div className='flex-1'>
          <Progress
            percent={(monitor.uptime || 0) * 100}
            showInfo={false}
            aria-label={`${monitor.name} uptime`}
            stroke={getUptimeStatusColor(monitor.status)}
          />
        </div>
      </div>
    </div>
  );

  return Object.entries(grouped).map(([gname, list]) => (
    <div key={gname || 'default'} className='mb-2'>
      {gname && (
        <>
          <div className='text-md font-semibold text-gray-500 px-2 py-1'>
            {gname}
          </div>
          <Divider />
        </>
      )}
      {list.map(renderItem)}
    </div>
  ));
};

// ========== 数据处理函数 ==========
export const processRawData = (
  data,
  dataExportDefaultTime,
  initializeMaps,
  updateMapValue,
) => {
  const result = {
    totalQuota: 0,
    totalTimes: 0,
    totalTokens: 0,
    uniqueModels: new Set(),
    timePoints: [],
    timeQuotaMap: new Map(),
    timeTokensMap: new Map(),
    timeCountMap: new Map(),
  };

  // 检查数据是否跨年
  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  data.forEach((item) => {
    result.uniqueModels.add(item.model_name);
    result.totalTokens += item.token_used;
    result.totalQuota += item.quota;
    result.totalTimes += item.count;

    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    if (!result.timePoints.includes(timeKey)) {
      result.timePoints.push(timeKey);
    }

    initializeMaps(
      timeKey,
      result.timeQuotaMap,
      result.timeTokensMap,
      result.timeCountMap,
    );
    updateMapValue(result.timeQuotaMap, timeKey, item.quota);
    updateMapValue(result.timeTokensMap, timeKey, item.token_used);
    updateMapValue(result.timeCountMap, timeKey, item.count);
  });

  result.timePoints.sort();
  return result;
};

export const calculateTrendData = (
  timePoints,
  timeQuotaMap,
  timeTokensMap,
  timeCountMap,
  dataExportDefaultTime,
) => {
  const quotaTrend = timePoints.map((time) => timeQuotaMap.get(time) || 0);
  const tokensTrend = timePoints.map((time) => timeTokensMap.get(time) || 0);
  const countTrend = timePoints.map((time) => timeCountMap.get(time) || 0);

  const rpmTrend = [];
  const tpmTrend = [];

  if (timePoints.length >= 2) {
    const interval = getTimeInterval(dataExportDefaultTime);

    for (let i = 0; i < timePoints.length; i++) {
      rpmTrend.push(timeCountMap.get(timePoints[i]) / interval);
      tpmTrend.push(timeTokensMap.get(timePoints[i]) / interval);
    }
  }

  return {
    balance: [],
    usedQuota: [],
    requestCount: [],
    times: countTrend,
    consumeQuota: quotaTrend,
    tokens: tokensTrend,
    rpm: rpmTrend,
    tpm: tpmTrend,
  };
};

export const aggregateDataByTimeAndModel = (data, dataExportDefaultTime) => {
  const aggregatedData = new Map();

  // 检查数据是否跨年
  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  data.forEach((item) => {
    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    const modelKey = item.model_name;
    const key = `${timeKey}-${modelKey}`;

    if (!aggregatedData.has(key)) {
      aggregatedData.set(key, {
        time: timeKey,
        model: modelKey,
        quota: 0,
        count: 0,
      });
    }

    const existing = aggregatedData.get(key);
    existing.quota += item.quota;
    existing.count += item.count;
  });

  return aggregatedData;
};

export const generateChartTimePoints = (
  aggregatedData,
  data,
  dataExportDefaultTime,
) => {
  let chartTimePoints = Array.from(
    new Set([...aggregatedData.values()].map((d) => d.time)),
  );

  if (chartTimePoints.length < DEFAULTS.MAX_TREND_POINTS) {
    const lastTime = Math.max(...data.map((item) => item.created_at));
    const interval = getTimeInterval(dataExportDefaultTime, true);

    // 生成时间点数组，用于检查是否跨年
    const generatedTimestamps = Array.from(
      { length: DEFAULTS.MAX_TREND_POINTS },
      (_, i) => lastTime - (6 - i) * interval,
    );
    const showYear = isDataCrossYear(generatedTimestamps);

    chartTimePoints = generatedTimestamps.map((ts) =>
      timestamp2string1(ts, dataExportDefaultTime, showYear),
    );
  }

  return chartTimePoints;
};

// ========== 用户维度数据处理 ==========
export const processUserData = (data, dataExportDefaultTime, limit = 10) => {
  const userQuotaTotal = new Map();
  data.forEach((item) => {
    const prev = userQuotaTotal.get(item.username) || 0;
    userQuotaTotal.set(item.username, prev + item.quota);
  });

  const sorted = Array.from(userQuotaTotal.entries()).sort(
    (a, b) => b[1] - a[1],
  );
  const topUsers = sorted.slice(0, limit).map(([u]) => u);
  const topUserSet = new Set(topUsers);

  const rankingData = sorted.slice(0, limit).map(([username, quota]) => ({
    User: username,
    Quota: quota,
  }));

  const showYear = isDataCrossYear(data.map((item) => item.created_at));

  const timeUserMap = new Map();
  const allTimePoints = new Set();

  data.forEach((item) => {
    const timeKey = timestamp2string1(
      item.created_at,
      dataExportDefaultTime,
      showYear,
    );
    allTimePoints.add(timeKey);
    const user = topUserSet.has(item.username) ? item.username : null;
    if (!user) return;
    const key = `${timeKey}-${user}`;
    const prev = timeUserMap.get(key) || { quota: 0 };
    timeUserMap.set(key, { quota: prev.quota + item.quota });
  });

  const sortedTimePoints = Array.from(allTimePoints).sort();
  const trendData = [];
  sortedTimePoints.forEach((time) => {
    topUsers.forEach((user) => {
      const key = `${time}-${user}`;
      const val = timeUserMap.get(key);
      trendData.push({
        Time: time,
        User: user,
        Quota: val?.quota || 0,
      });
    });
  });

  return { rankingData, trendData, topUsers };
};
