import React, { useContext, useEffect } from 'react';
import { Outlet } from 'react-router-dom';

import { StatusContext } from '@classic/context/Status';
import { UserContext } from '@classic/context/User';
import { API, updateAPI } from '@classic/helpers/api';
import { setStatusData } from '@classic/helpers/data';

// 应用启动引导：拉取 /api/status 写入 context + localStorage（复用 hooks
// 依赖 StatusContext 里的模型配置），并从 localStorage 恢复登录态。
const Root = () => {
  const [, statusDispatch] = useContext(StatusContext);
  const [, userDispatch] = useContext(UserContext);

  useEffect(() => {
    // 邀请码落盘：分享链接是 /m/register?aff=xxx，但用户可能先逛别的页面再去注册，
    // 那时 query 早没了。这里在应用启动就把它存下来，注册页兜底读取。
    // key 与 classic / default 主题一致（都是 'aff'）。
    const aff = new URLSearchParams(window.location.search).get('aff')?.trim();
    if (aff) {
      localStorage.setItem('aff', aff);
    }

    const stored = localStorage.getItem('user');
    if (stored) {
      try {
        userDispatch({ type: 'login', payload: JSON.parse(stored) });
        updateAPI();
      } catch (e) {
        localStorage.removeItem('user');
      }
    }

    const loadStatus = async () => {
      try {
        const res = await API.get('/api/status');
        const { success, data } = res.data;
        if (success) {
          statusDispatch({ type: 'set', payload: data });
          setStatusData(data);
          if (data.system_name) {
            document.title = data.system_name;
            const appleTitle = document.querySelector(
              "meta[name='apple-mobile-web-app-title']",
            );
            if (appleTitle) {
              appleTitle.content = data.system_name;
            }
          }
          // 浏览器 favicon 与 iOS 添加到主屏幕图标均跟随运营配置的 logo
          if (data.logo) {
            const link = document.querySelector("link[rel~='icon']");
            if (link) {
              link.href = data.logo;
            }
            const appleTouchIcon = document.querySelector(
              "link[rel='apple-touch-icon']",
            );
            if (appleTouchIcon) {
              appleTouchIcon.href = data.logo;
            }
          }
        }
      } catch (e) {
        console.error('加载站点状态失败', e);
      }
    };
    loadStatus();
  }, [statusDispatch, userDispatch]);

  return <Outlet />;
};

export default Root;
