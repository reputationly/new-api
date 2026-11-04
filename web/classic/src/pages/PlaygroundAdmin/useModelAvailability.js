// 「体验区管理」两个页面共用：当前**有可用渠道**的模型名集合。
//
// 渠道停用或模型从渠道删掉之后，配置仍然留在各份 ModelConfig 里 —— 对新请求毫无作用，
// 却让「分类页」和每个玩法页越堆越长，运营得翻很久才能找到要改的那一条。
//
// 放在这里而不是两个面板各写一份：判据一旦分叉，就会出现"分类页说它没渠道、玩法页却说
// 有"这种自相矛盾的界面。同一份请求、同一份缓存。
//
// 返回 null 表示**还不知道**（请求未返回或失败）。调用方必须 fail-open、一律不折叠 ——
// 折叠是"把东西藏起来"的动作，只能建立在确定的信息上：一次 DB 抖动就把运营的配置从
// 页面上藏掉，比翻得久糟得多。
import { useEffect, useState } from 'react';

import { API } from '../../helpers';

const ENDPOINT = '/api/playground_admin/model_availability';

// cache: Set<string> | null（null = 查不到，调用方据此不折叠）
// pending: 同一个页面上两个面板可能同时挂载，共用一次请求
let cache = null;
let pending = null;

const loadAvailability = () => {
  if (!pending) {
    pending = API.get(ENDPOINT)
      .then((res) => {
        const list = res?.data?.data?.models;
        if (Array.isArray(list)) cache = new Set(list);
        return cache;
      })
      .catch(() => null); // 失败：cache 保持 null，下次挂载可重试
    pending.then(() => {
      pending = null;
    });
  }
  return pending;
};

export const useModelAvailability = () => {
  const [names, setNames] = useState(cache);
  useEffect(() => {
    if (cache) {
      setNames(cache);
      return undefined;
    }
    let cancelled = false;
    loadAvailability().then((v) => {
      if (!cancelled) setNames(v);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return names;
};

// 仅供测试：清掉模块级缓存，避免用例之间互相影响。
export const resetModelAvailabilityCache = () => {
  cache = null;
  pending = null;
};

// activeRows / inactiveRows 的分组规则也放这里，两个面板共用同一套判定。
export const splitByAvailability = (rows, available) => {
  if (!available) return { activeRows: rows, inactiveRows: [] };
  return {
    activeRows: rows.filter((r) => available.has(r.name)),
    inactiveRows: rows.filter((r) => !available.has(r.name)),
  };
};
