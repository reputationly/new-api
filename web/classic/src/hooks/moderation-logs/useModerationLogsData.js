import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, showError, timestamp2string } from '../../helpers';
import { ITEMS_PER_PAGE } from '../../constants';
import { useTableCompactMode } from '../common/useTableCompactMode';

// 审核记录页的数据层。见 docs/content-moderation-design.md §10.1。

export const useModerationLogsData = () => {
  const { t } = useTranslation();

  const [logs, setLogs] = useState([]);
  const [loading, setLoading] = useState(false);
  const [activePage, setActivePage] = useState(1);
  const [logCount, setLogCount] = useState(0);
  const [pageSize, setPageSize] = useState(ITEMS_PER_PAGE);
  const [formApi, setFormApi] = useState(null);
  const [compactMode, setCompactMode] = useTableCompactMode('moderationLogs');

  // 原文弹窗。内容不进 logs 行对象：每次取原文后端都会写一条管理操作审计，
  // 缓存下来重开不请求，就等于「看了但没留痕」，而留痕是这个入口唯一的约束。
  const [contentModalOpen, setContentModalOpen] = useState(false);
  const [contentLoading, setContentLoading] = useState(false);
  const [contentText, setContentText] = useState('');

  // 媒体弹窗。同样不缓存 URL：每次都要写审计，缓存等于「看了但没留痕」。
  // 而且签名链接是短期的（取证桶默认 1 小时），缓存下来重开多半已经过期。
  const [mediaModalOpen, setMediaModalOpen] = useState(false);
  const [mediaLoading, setMediaLoading] = useState(false);
  const [mediaUrl, setMediaUrl] = useState('');
  const [mediaIsVideo, setMediaIsVideo] = useState(false);

  const now = new Date();
  const zeroNow = new Date(now.getFullYear(), now.getMonth(), now.getDate());

  // 默认看今天：这张表的典型用法是「刚有人反馈发不出去」，不是翻旧账。
  const formInitValues = {
    dateRange: [
      timestamp2string(zeroNow.getTime() / 1000),
      timestamp2string(now.getTime() / 1000 + 3600),
    ],
    username: '',
    word: '',
    action: '',
    source: '',
    category: '',
    model_name: '',
    group: '',
    channel_id: '',
    request_id: '',
  };

  const getFormValues = () => {
    const values = formApi ? formApi.getValues() : {};
    let start = formInitValues.dateRange[0];
    let end = formInitValues.dateRange[1];
    if (Array.isArray(values.dateRange) && values.dateRange.length === 2) {
      start = values.dateRange[0];
      end = values.dateRange[1];
    }
    return { ...values, start_timestamp: start, end_timestamp: end };
  };

  const loadLogs = async (page = 1, size = pageSize) => {
    setLoading(true);
    const values = getFormValues();
    const params = new URLSearchParams({
      p: page,
      page_size: size,
      start_timestamp: parseInt(Date.parse(values.start_timestamp) / 1000),
      end_timestamp: parseInt(Date.parse(values.end_timestamp) / 1000),
    });
    // 命中词可能带 & = 之类的字符，这里必须走 URLSearchParams 而不是手拼，
    // 否则一条含 & 的词会把后面的筛选条件整个吃掉。
    [
      'username',
      'word',
      'action',
      'source',
      'category',
      'model_name',
      'group',
      'channel_id',
      'request_id',
    ].forEach((key) => {
      if (values[key]) params.set(key, values[key]);
    });

    try {
      const res = await API.get(`/api/moderation/logs?${params.toString()}`);
      const { success, message, data } = res.data;
      if (success) {
        setLogs(
          (data.items || []).map((log) => ({ ...log, key: '' + log.id })),
        );
        setLogCount(data.total || 0);
        setActivePage(data.page || 1);
        setPageSize(data.page_size || size);
      } else {
        showError(message);
      }
    } finally {
      setLoading(false);
    }
  };

  const handlePageChange = (page) => {
    loadLogs(page, pageSize).then();
  };

  const handlePageSizeChange = async (size) => {
    localStorage.setItem('moderation-logs-page-size', size + '');
    await loadLogs(1, size);
  };

  const refresh = async () => {
    await loadLogs(1, pageSize);
  };

  const openContentModal = async (log) => {
    setContentText('');
    setContentModalOpen(true);
    setContentLoading(true);
    try {
      const res = await API.get(`/api/moderation/logs/${log.id}/content`);
      const { success, message, data } = res.data;
      if (success) {
        setContentText(data?.content || '');
      } else {
        showError(message);
        setContentModalOpen(false);
      }
    } catch (e) {
      // 请求本身没跑完（网络故障 / 5xx）。拦截器已经弹过 toast，这里必须把弹窗关掉：
      // 留在原地会渲染成「（无内容）」，而后端表达「这条没留原文」用的是 success:false
      // （HTTP 200），根本走不到这个分支——等于把一次失败的查询显示成确定的结论，
      // 恰好骗过唯一一个能回答「到底有没有留原文」的地方。
      setContentModalOpen(false);
    } finally {
      setContentLoading(false);
    }
  };

  const openMediaModal = async (log) => {
    setMediaUrl('');
    // modality 决定用 <img> 还是 <video> 渲染。视频记录的取证对象是原始视频，
    // 不是抽出来的帧——帧只在判定时用，没有留存。
    setMediaIsVideo(log.modality === 'video');
    setMediaModalOpen(true);
    setMediaLoading(true);
    try {
      const res = await API.get(`/api/moderation/logs/${log.id}/media`);
      const { success, message, data } = res.data;
      if (success) {
        setMediaUrl(data?.url || '');
      } else {
        showError(message);
        setMediaModalOpen(false);
      }
    } catch (e) {
      // 与取原文同理：请求没跑完时必须关掉弹窗，留在原地会把一次失败的查询
      // 显示成「没有留存」这个确定结论。
      setMediaModalOpen(false);
    } finally {
      setMediaLoading(false);
    }
  };

  const closeMediaModal = () => {
    setMediaModalOpen(false);
    // 立刻丢掉 URL：它是一条能直接打开违规内容的链接，没必要在内存里多留一秒。
    setMediaUrl('');
  };

  const closeContentModal = () => {
    setContentModalOpen(false);
    setContentText('');
  };

  useEffect(() => {
    const localPageSize =
      parseInt(localStorage.getItem('moderation-logs-page-size')) ||
      ITEMS_PER_PAGE;
    setPageSize(localPageSize);
    loadLogs(1, localPageSize).then();
  }, []);

  return {
    logs,
    loading,
    activePage,
    logCount,
    pageSize,

    formApi,
    setFormApi,
    formInitValues,

    compactMode,
    setCompactMode,

    contentModalOpen,
    contentLoading,
    contentText,
    openContentModal,
    mediaModalOpen,
    mediaLoading,
    mediaUrl,
    mediaIsVideo,
    openMediaModal,
    closeMediaModal,
    closeContentModal,

    loadLogs,
    handlePageChange,
    handlePageSizeChange,
    refresh,

    t,
  };
};
