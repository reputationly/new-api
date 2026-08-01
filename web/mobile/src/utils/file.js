export const fileToDataUrl = (file) =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });

// 取不到时长时最多等多久。本地 blob 读元数据本该是瞬时的，超过就当读不到。
const MEDIA_DURATION_TIMEOUT_MS = 3000;

// 读取音/视频时长（秒）。把 objectURL 挂到临时元素上取 metadata，读完立刻释放。
// 取不到时返回 NaN —— 调用方据此放行，不能因为拿不到时长就把用户的文件拦下来
// （部分安卓 WebView 对某些容器不吐 duration），体积上限仍会兜底。
//
// 必须有超时：preload='metadata' 只是提示，开了省流/低电量的 WebView 会推迟到
// play() 才解析元数据，且此时 error 也不触发 —— 只挂 loadedmetadata/error 的话
// Promise 永不 settle，调用方就卡在 await 上，用户录完视频界面毫无反应也无报错。
export const readMediaDuration = (file, kind = 'video') =>
  new Promise((resolve) => {
    let url = '';
    let settled = false;
    const el = document.createElement(kind === 'audio' ? 'audio' : 'video');
    let timer = null;
    const done = (value) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      el.onloadedmetadata = null;
      el.onerror = null;
      if (url) URL.revokeObjectURL(url);
      resolve(value);
    };
    try {
      url = URL.createObjectURL(file);
    } catch (e) {
      resolve(NaN);
      return;
    }
    timer = setTimeout(() => {
      // 断开 src 让 WebView 停止取数据，否则元素会连着 objectURL 一起挂在那。
      el.removeAttribute('src');
      done(NaN);
    }, MEDIA_DURATION_TIMEOUT_MS);
    el.preload = 'metadata';
    el.onloadedmetadata = () => done(el.duration);
    el.onerror = () => done(NaN);
    el.src = url;
  });
