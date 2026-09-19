import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { InfiniteScroll, List, NavBar, PullToRefresh, Tag } from 'antd-mobile';
import dayjs from 'dayjs';

import { API } from '@classic/helpers/api';
import { DISCOUNT_HEX } from '@classic/helpers/discount';

import { showError } from '../shims/classic-utils';
import { renderQuota } from '../utils/quota';

const PAGE_SIZE = 20;

// 日志类型：与后端 model/log.go 对齐（1 充值 2 消费 3 管理 4 系统 5 错误 6 退款）
const typeTag = (type) => {
  switch (type) {
    case 1:
      return <Tag color='success'>充值</Tag>;
    case 2:
      return <Tag color='primary'>消费</Tag>;
    case 3:
      return <Tag color='default'>管理</Tag>;
    case 4:
      return <Tag color='default'>系统</Tag>;
    case 5:
      return <Tag color='danger'>错误</Tag>;
    case 6:
      return <Tag color='warning'>退款</Tag>;
    default:
      return <Tag color='default'>其他</Tag>;
  }
};

// other 是后端存的 JSON 字符串。只取 time_rule 一项，解析失败就当没有——
// 日志页为一个装饰性字段报错是本末倒置。
const timeRuleOf = (log) => {
  if (!log?.other) return '';
  try {
    return JSON.parse(log.other)?.time_rule || '';
  } catch {
    return '';
  }
};

// 与 PC 端时段角标同色（@classic/helpers/discount 的 DISCOUNT_HEX.cyan.fg）。
const TIME_RULE_COLOR = DISCOUNT_HEX.cyan.fg;

const Logs = () => {
  const navigate = useNavigate();
  const [logs, setLogs] = useState([]);
  const [hasMore, setHasMore] = useState(true);
  const pageRef = useRef(1);

  const loadPage = useCallback(async (page) => {
    const res = await API.get(
      `/api/log/self?p=${page}&page_size=${PAGE_SIZE}&type=0`,
    );
    const { success, message, data } = res.data;
    if (!success) {
      showError(message);
      return [];
    }
    return data.items || [];
  }, []);

  const refresh = useCallback(async () => {
    try {
      const items = await loadPage(1);
      pageRef.current = 1;
      setLogs(items);
      setHasMore(items.length >= PAGE_SIZE);
    } catch (e) {
      showError(e);
    }
  }, [loadPage]);

  const loadMore = useCallback(async () => {
    const next = pageRef.current + 1;
    const items = await loadPage(next);
    pageRef.current = next;
    setLogs((prev) => [...prev, ...items]);
    setHasMore(items.length >= PAGE_SIZE);
  }, [loadPage]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div>
      <NavBar onBack={() => navigate(-1)}>使用日志</NavBar>
      <PullToRefresh onRefresh={refresh}>
        <List>
          {logs.map((log, idx) => (
            <List.Item
              key={`${log.id || idx}`}
              title={
                <span>
                  {typeTag(log.type)} {log.model_name || log.token_name || ''}
                </span>
              }
              description={dayjs(log.created_at * 1000).format(
                'MM-DD HH:mm:ss',
              )}
              extra={log.type === 2 ? `-${renderQuota(log.quota || 0)}` : ''}
            >
              {log.type === 2 && (
                <span style={{ fontSize: 12, color: 'var(--adm-color-weak)' }}>
                  输入 {log.prompt_tokens} / 输出 {log.completion_tokens}
                </span>
              )}
              {log.content && (
                <div
                  style={{
                    fontSize: 12,
                    color: 'var(--adm-color-weak)',
                    wordBreak: 'break-all',
                  }}
                >
                  {log.content}
                </div>
              )}
              {/*
                时段折扣。这是本页唯一读 other 的地方——用户要能自己验证「夜里下单
                确实便宜了」，而 content 那串算式里没有这一项（group_ratio 已含时段
                系数，但看不出其中有多少来自时段）。
              */}
              {timeRuleOf(log) && (
                <div style={{ fontSize: 12, color: TIME_RULE_COLOR }}>
                  时段折扣 {timeRuleOf(log)}
                </div>
              )}
            </List.Item>
          ))}
        </List>
        <InfiniteScroll loadMore={loadMore} hasMore={hasMore} />
      </PullToRefresh>
    </div>
  );
};

export default Logs;
