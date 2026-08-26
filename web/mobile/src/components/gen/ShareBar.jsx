import React, { useState } from 'react';
import { Button, Dialog, Toast } from 'antd-mobile';

import { API } from '@classic/helpers/api';

import {
  canShareFiles,
  copyToClipboard,
  isWeChatBrowser,
  shareMediaUrl,
} from '../../utils/share';

// 弹一个只放「真实 <a href>」的框，让用户自己点那一下触发下载。
//
// 不能拿到直链就 window.location.href 跳过去：那已经在 await 之后，没有用户手势，
// 夸克/UC 会静默拦掉（点了毫无反应，不报错也不下载）。用户点这个链接是全新的手势，
// 任何浏览器都拦不住。
//
// 不加 download 属性：签名直链是跨域的，download 本就会被浏览器忽略；真正让它「保存」
// 而不是「播放」的是后端签 URL 时带上的 Content-Disposition: attachment
// （controller/task_download.go 的 WithDownloadName）。
const showDownloadLink = (url) => {
  const handler = Dialog.show({
    title: '保存到本地',
    content: (
      <div style={{ textAlign: 'center', padding: '4px 0' }}>
        <a
          href={url}
          rel='noopener'
          onClick={() => handler.close()}
          style={{
            display: 'inline-block',
            padding: '10px 20px',
            fontSize: 16,
          }}
        >
          点此下载
        </a>
        <div
          style={{
            marginTop: 6,
            fontSize: 12,
            color: 'var(--adm-color-weak)',
          }}
        >
          点上面的链接开始下载
        </div>
      </div>
    ),
    closeOnMaskClick: true,
    // **closeOnAction 必须显式开**：antd-mobile 的 Dialog 默认是 false，actions 里的
    // 按钮点了只触发 onClick、不关框 —— 而这里的「取消」没有 onClick，于是点了毫无
    // 反应，用户被困在弹框里（遮罩点击是唯一出路，但没人猜得到）。
    // 仓库里其他带自定义 actions 的 Dialog（Tokens.jsx、AdminKyc.jsx 等）都显式写了这条。
    closeOnAction: true,
    actions: [{ key: 'cancel', text: '取消' }],
  });
};

// 生成结果下方的「保存」操作条。
//
// 微信内置浏览器不给 <video>/<audio> 长按保存菜单、忽略 a[download]、禁用
// navigator.share —— 音视频在微信里没有任何本地保存路径，这是平台限制。所以传了
// taskId 的音视频在微信下改走 openDownloadPage：跳到免登录下载页 /s/<token>，
// 由那页引导用户「···」→「在浏览器打开」后下载。图片不受此限（微信原生支持长按
// 保存/转发），保持原提示。
//
// 那条 /s/<token> 落地页**只提供下载、不内嵌播放器**（见 controller/share_link.go）：
// 内容审核尚不完善，站外不放在线浏览。所以本文件文案一律说「保存 / 下载」，
// 不写「分享」「观看」——按钮不该暗示「把一条能在线播放的链接发出去」。
// 拿这条任务成品的签名直链（带 Content-Disposition: attachment）。
// 拿不到不是致命错——调用方后面还有 blob 兜底，所以吞掉异常返回空串即可。
const fetchDownloadUrl = async (taskId) => {
  try {
    const res = await API.get(
      `/api/task/self/${encodeURIComponent(taskId)}/download`,
    );
    const { success, data } = res.data;
    return success && data.attachment ? data.url : '';
  } catch (e) {
    return '';
  }
};

// 走 a[download] 之后的提示。不能报「已保存」：夸克/UC/微信这类 WebView 直接忽略
// download 属性，不报错也不下载，谎报成功比什么都不说更糟——用户会以为文件已在相册
// 里而不再去找。所以只说「已触发」，并给出下一步。
const showDownloadTriggeredHint = () => {
  Toast.show({
    content: '已触发下载；若没有反应，请长按视频保存或用系统浏览器打开本页',
    duration: 4000,
  });
};

const ShareBar = ({ url, filename, hint, taskId }) => {
  const [busy, setBusy] = useState(false);

  // 微信里的保存路径：把用户送到免登录下载页 /s/<token>。
  //
  // 那页是**匿名**的，所以用户在微信里点右上角「···」→「在浏览器打开」，系统浏览器
  // 打开的就是同一页（不需要登录），点一下「下载到本地」即得文件。页面本身也印着
  // 这句引导，不必靠 Toast 交代。
  //
  // 从「复制链接让用户自己粘到浏览器」改成直接跳页：那条路多一个剪贴板环节，而
  // 微信/iOS 在 await 之后经常直接拒写剪贴板（本文件顶部与 utils/share.js 都记过这个坑），
  // 一旦拒掉用户就只剩一个满是 token 的长链接要手动长按复制。跳页没有这个易碎点——
  // 而且 /s/<token> 是同源 HTML 页面，不是下载类导航，不会被国产 WebView 的手势门控拦掉。
  const openDownloadPage = async () => {
    setBusy(true);
    try {
      const res = await API.post(
        `/api/task/self/${encodeURIComponent(taskId)}/share`,
      );
      const { success, message, data } = res.data;
      if (!success) {
        throw new Error(message || '获取下载链接失败');
      }
      // 两种形态：
      // - data.path：常规的 /s/<token> 免登录下载页。/s/* 由 Go 二进制直出，
      //   外置前端部署下它跟着后端走而不是当前页面的 origin，基址要和 API 客户端
      //   保持同一套解析规则。
      // - data.url：渠道开了「透传成品地址」，成品本就是公网直链，后端原样给回，
      //   不经我方中转（也因此继承上游有效期，通常 24 小时）。这条加不了
      //   Content-Disposition，是「站外只能下载」的已知缺口，见后端同名注释；
      //   也正因为跳过去只会当场播放，这一支不跳页，仍旧退回给用户一条链接。
      const base =
        import.meta.env.VITE_REACT_APP_SERVER_URL || window.location.origin;
      if (data.path) {
        window.location.href = base.replace(/\/$/, '') + data.path;
        return;
      }

      const link = data.url;
      if (await copyToClipboard(link)) {
        Toast.show({ content: '链接已复制，粘到手机浏览器打开即可保存' });
        return;
      }
      // 上面这次复制多半是因为 await 之后已经脱离了用户手势上下文——iOS 与微信
      // WebView 都会在这种情况下拒绝写剪贴板。所以兜底不是「报个失败」，而是给一个
      // 按钮：点它是一次全新的手势，复制通常就成了；再不行还能长按选中文本。
      Dialog.confirm({
        title: '复制链接',
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
        content: e.message || '获取下载链接失败，请重试',
      });
    } finally {
      setBusy(false);
    }
  };

  const handleShare = async () => {
    if (isWeChatBrowser()) {
      if (taskId) {
        await openDownloadPage();
        return;
      }
      Dialog.alert({
        title: '在微信中保存',
        content:
          hint ||
          '微信内置浏览器不支持直接保存文件：图片可长按保存；视频/音频请点右上角「···」选择在浏览器打开后再保存。',
      });
      return;
    }

    setBusy(true);
    try {
      // **这个函数的每一条路径都必须留下可见结果**。此前夸克上点了毫无反应，就是因为
      // 下面这个 canShareFiles() 探针返回 true（夸克是 Chromium 内核，canShare({files})
      // 认），于是签名直链那条路被整个跳过；可真调 navigator.share() 时 WebView 并没有
      // 对接系统分享，直接 reject，我们把它当「用户取消」就静默结束了——用户唯一感知
      // 到的只有几秒 loading。探针不可信，那就别让它决定成败。
      const canShare = canShareFiles();

      // 系统分享面板仍然优先试：「存到相册 / 直接发好友」是纯下载给不了的能力
      // （iOS 等），值得为它把文件读进内存。
      if (canShare) {
        const result = await shareMediaUrl(url, filename);
        if (result === 'shared') return; // 系统面板已弹过，用户看得见
        if (result === 'downloaded') {
          showDownloadTriggeredHint();
          return;
        }
        // 'cancelled'：可能是用户真按了取消，也可能是 WebView 假装支持。分不出来，
        // 那就往下走给一条明确的路——多一个框，好过什么都没有。
      }

      // 带 attachment 的签名直链：公网直链、不需要 cookie、不占内存，是最可靠的一条路。
      // 但只有落了 OBS 的成品才签得出来；媒体存储没开、落盘失败或老数据拿到的是裸链，
      // 点过去只会当场播放，那种情况得退回 blob。
      const downloadUrl = taskId ? await fetchDownloadUrl(taskId) : '';
      if (downloadUrl) {
        showDownloadLink(downloadUrl);
        return;
      }

      // 能分享却取消了、又没有直链可给：如实说取消，不再装作什么都没发生。
      if (canShare) {
        Toast.show({ content: '已取消保存' });
        return;
      }

      // 最后的兜底：读 blob 走 a[download]。成不成取决于浏览器，故提示只说「已触发」。
      const result = await shareMediaUrl(url, filename);
      if (result === 'shared') return;
      showDownloadTriggeredHint();
    } catch (e) {
      Toast.show({ icon: 'fail', content: '保存失败，请重试' });
    } finally {
      setBusy(false);
    }
  };

  // 文案固定「保存」，不再随浏览器变、也不再出现「分享」二字：站外能拿到的只有
  // 下载，不提供在线浏览，按钮就不该暗示「把链接发出去」这个动作。
  return (
    <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
      <Button size='mini' fill='outline' loading={busy} onClick={handleShare}>
        保存
      </Button>
    </div>
  );
};

export default ShareBar;
