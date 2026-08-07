import React, { useState } from 'react';
import { Button, Dialog, Toast } from 'antd-mobile';

import { API } from '@classic/helpers/api';

import {
  canShareFiles,
  copyToClipboard,
  isWeChatBrowser,
  navigateToDownload,
  shareMediaUrl,
} from '../../utils/share';

// 生成结果下方的分享/保存操作条。
//
// 微信内置浏览器不给 <video>/<audio> 长按保存菜单、忽略 a[download]、禁用
// navigator.share —— 音视频在微信里没有任何本地保存路径，这是平台限制。所以传了
// taskId 的音视频在微信下改走「复制免登录分享链接」：链接粘到外部浏览器即可观看
// 下载，也能直接发给好友。图片不受此限（微信原生支持长按保存/转发），保持原提示。
const ShareBar = ({ url, filename, hint, taskId }) => {
  const [busy, setBusy] = useState(false);

  const shareViaLink = async () => {
    setBusy(true);
    try {
      const res = await API.post(
        `/api/task/self/${encodeURIComponent(taskId)}/share`,
      );
      const { success, message, data } = res.data;
      if (!success) {
        throw new Error(message || '生成分享链接失败');
      }
      // 两种形态：
      // - data.url：渠道开了「透传成品地址」，成品本就是公网直链，后端原样给回，
      //   不经我方中转（也因此继承上游有效期，通常 24 小时）。
      // - data.path：常规的 /s/<token> 免登录落地页。/s/* 由 Go 二进制直出，
      //   外置前端部署下它跟着后端走而不是当前页面的 origin，基址要和 API 客户端
      //   保持同一套解析规则。
      const base =
        import.meta.env.VITE_REACT_APP_SERVER_URL || window.location.origin;
      const link = data.url || base.replace(/\/$/, '') + data.path;
      if (await copyToClipboard(link)) {
        Toast.show({ content: '链接已复制，粘到浏览器打开或发给好友' });
        return;
      }
      // 上面这次复制多半是因为 await 之后已经脱离了用户手势上下文——iOS 与微信
      // WebView 都会在这种情况下拒绝写剪贴板。所以兜底不是「报个失败」，而是给一个
      // 按钮：点它是一次全新的手势，复制通常就成了；再不行还能长按选中文本。
      Dialog.confirm({
        title: '复制分享链接',
        content: link,
        confirmText: '复制',
        cancelText: '关闭',
        onConfirm: async () => {
          if (await copyToClipboard(link)) {
            Toast.show({ content: '链接已复制' });
          } else {
            Toast.show({ content: '请长按上方链接手动复制' });
          }
        },
      });
    } catch (e) {
      Toast.show({
        icon: 'fail',
        content: e.message || '生成分享链接失败，请重试',
      });
    } finally {
      setBusy(false);
    }
  };

  const handleShare = async () => {
    if (isWeChatBrowser()) {
      if (taskId) {
        await shareViaLink();
        return;
      }
      Dialog.alert({
        title: '在微信中分享',
        content:
          hint ||
          '微信内置浏览器不支持直接分享文件：图片可长按转发；视频/音频请点右上角「···」选择在浏览器打开后再分享，或先保存到手机相册后发送。',
      });
      return;
    }

    setBusy(true);
    try {
      // 能把文件本体交给系统分享面板的（iOS 等）优先走它——「存到相册 / 直接发好友」
      // 值得为此把文件读进内存，这是纯下载给不了的能力。
      //
      // 其余环境（桌面、多数 Android）只需要下载，就没必要读内存：几十 MB 的成品走
      // fetch→blob 既有 OOM 风险，过程中也只有一个 loading、没有进度。改为导航到带
      // attachment 的直链。但只有落了 OBS 的成品才签得出 attachment，媒体存储没开、
      // 落盘失败或老数据拿到的是裸链，导航过去只会当场播放——那种情况必须退回 blob。
      if (taskId && !canShareFiles()) {
        const res = await API.get(
          `/api/task/self/${encodeURIComponent(taskId)}/download`,
        );
        const { success, data } = res.data;
        if (success && data.attachment) {
          navigateToDownload(data.url);
          return;
        }
      }

      const result = await shareMediaUrl(url, filename);
      if (result === 'downloaded') {
        Toast.show({ content: '已保存，可在微信中发送' });
      }
    } catch (e) {
      Toast.show({ icon: 'fail', content: '分享失败，请重试' });
    } finally {
      setBusy(false);
    }
  };

  const label = isWeChatBrowser() && taskId ? '复制分享链接' : '分享 / 保存';

  return (
    <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
      <Button size='mini' fill='outline' loading={busy} onClick={handleShare}>
        {label}
      </Button>
    </div>
  );
};

export default ShareBar;
