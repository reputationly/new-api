export const fileToDataUrl = (file) =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });

// 上传图片先缩到这个长边再转 data-url。手机直出照片常是 4000×3000 / 3~8MB，而体验区
// 的输出最高 1080P，原图那些像素一路上到引擎也会被 resize 掉，纯属白扛：
//   - 渲染：<Image width={28}> 并不省解码 —— 浏览器仍要把整张 4000×3000 解成
//     ~48MB 位图再缩，两张关键帧就是近百 MB，低端机上就是肉眼可见的卡顿；
//   - 存储：每次 persist 都要把 data-url atob 成 Blob 存 IDB，体积翻倍地耗主线程；
//   - 提交：base64 比原文件再大 33%，请求体和后端物化都跟着慢。
// 2048 对 720P/1080P 绰绰有余(1080P 长边 1920)，质量 0.85 是肉眼无损区间。
const IMAGE_MAX_EDGE = 2048;
const IMAGE_QUALITY = 0.85;

// 压缩图片为 JPEG data-url。任何一步失败都回退成原图 data-url —— 压缩是优化不是校验，
// 不能因为某台机器的 canvas/decode 行为异常就把用户的图挡下来。
// 已经比阈值小的图直接原样返回，避免无谓的重编码(那反而可能变大、且丢一次质量)。
export const imageFileToDataUrl = async (file) => {
  const raw = await fileToDataUrl(file);
  if (typeof createImageBitmap !== 'function') return raw;
  let bitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch (e) {
    return raw;
  }
  try {
    const { width, height } = bitmap;
    const edge = Math.max(width, height);
    if (!edge || edge <= IMAGE_MAX_EDGE) return raw;
    const scale = IMAGE_MAX_EDGE / edge;
    const w = Math.round(width * scale);
    const h = Math.round(height * scale);
    const canvas = document.createElement('canvas');
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d');
    if (!ctx) return raw;
    ctx.drawImage(bitmap, 0, 0, w, h);
    const out = canvas.toDataURL('image/jpeg', IMAGE_QUALITY);
    // 极少数情况(如原图本就是高压缩比 WebP)重编码后反而更大,那就用原图。
    return out && out.length < raw.length ? out : raw;
  } catch (e) {
    return raw;
  } finally {
    bitmap.close?.();
  }
};

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
