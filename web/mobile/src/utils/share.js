// 生成结果分享：优先系统分享面板（可直接发微信/朋友圈），
// 不支持时回退为下载保存；微信内置浏览器由调用方引导（长按/外部浏览器）。

export const isWeChatBrowser = () =>
  /MicroMessenger/i.test(navigator.userAgent);

// 能不能把「文件本体」交给系统分享面板（iOS Safari / 部分 Android 可以）。
//
// 用一个空 File 探能力而不是嗅 UA：我们真正关心的就是 canShare({files}) 的结果，
// 直接问浏览器比猜平台准。能分享文件才值得把整个视频读进内存换「存到相册」，
// 否则应该走服务端下载，零内存。
export function canShareFiles() {
  try {
    if (typeof navigator.share !== 'function' || !navigator.canShare) {
      return false;
    }
    const probe = new File([], 'probe.mp4', { type: 'video/mp4' });
    return navigator.canShare({ files: [probe] });
  } catch (e) {
    return false;
  }
}

// 导航到一条带 Content-Disposition: attachment 的直链来触发下载，全程不读文件内容。
//
// 用 location 赋值而不是 a[download]：程序化下载会被 iOS/Safari 按用户手势门控，
// 跨过 await 就失效；而导航不受此限制，可以放心在拿到签名 URL 之后再调用。
// attachment 保证浏览器把它当下载处理，不会真的离开当前页面。
//
// 调用前必须确认该 URL 确实带 attachment（后端 /download 的 attachment 字段），
// 否则会当场播放媒体、甚至在 data: URL 上被浏览器拦截。
export function navigateToDownload(url) {
  window.location.href = url;
}

// 返回 'shared' | 'downloaded' | 'cancelled'
export async function shareMediaUrl(url, filename) {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`获取文件失败 (${res.status})`);
  }
  const blob = await res.blob();
  const file = new File([blob], filename, {
    type: blob.type || 'application/octet-stream',
  });
  if (
    typeof navigator.share === 'function' &&
    navigator.canShare &&
    navigator.canShare({ files: [file] })
  ) {
    try {
      await navigator.share({ files: [file] });
      return 'shared';
    } catch (e) {
      if (e && e.name === 'AbortError') return 'cancelled';
      // 部分浏览器 share 失败后仍可下载兜底
    }
  }
  const objectUrl = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = objectUrl;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(objectUrl);
  return 'downloaded';
}

// 复制文本到剪贴板，成功返回 true。
//
// 微信里的分享链接全靠它落地，所以要两级兜底：navigator.clipboard 需要安全上下文
// 且部分 WebView 会直接抛，此时退回 execCommand；两条都失败时调用方应把链接摊出来
// 让用户长按手动复制，绝不能只报个「失败」了事。
export async function copyToClipboard(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch (e) {
    // 落到 execCommand 兜底
  }

  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    // iOS WebView 上 readOnly 的 textarea 选不中，必须可编辑 + 手动建 Range；
    // 定位到视口外并置 0 字号，避免复制瞬间页面跳动或键盘弹出。
    ta.contentEditable = 'true';
    ta.readOnly = false;
    ta.style.position = 'fixed';
    ta.style.top = '-9999px';
    ta.style.fontSize = '16px'; // 小于 16px 时 iOS 会缩放页面
    ta.style.opacity = '0';
    document.body.appendChild(ta);

    const range = document.createRange();
    range.selectNodeContents(ta);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    ta.setSelectionRange(0, text.length);

    const ok = document.execCommand('copy');
    selection.removeAllRanges();
    document.body.removeChild(ta);
    return ok;
  } catch (e) {
    return false;
  }
}
