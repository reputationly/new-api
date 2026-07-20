// 体验区「一键示例」素材加载工具。
//
// 示例里的预置文件用 public 下的相对 URL 引用(/playground-samples/... 或
// /audio-presets/...)。体验区的文件输入统一是 base64 data-url(存进各 hook 的
// inputs,提交时透传给门面),所以点击示例时要把素材 URL 取回来编码成 data-url,
// 再写进对应的 inputs 字段——与用户手动上传(FileReader.readAsDataURL)落到同一形态。
//
// 同一 URL 只 fetch + 编码一次:浏览器 HTTP 缓存之上再加一层内存缓存,重复点击零成本。

const dataUrlCache = new Map();

function blobToDataUrl(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reject;
    reader.readAsDataURL(blob);
  });
}

// 把 public 下的素材 URL 转成 base64 data-url(带缓存)。失败抛错,由调用方兜底提示。
export async function urlToDataUrl(url) {
  if (dataUrlCache.has(url)) return dataUrlCache.get(url);
  const resp = await fetch(url);
  if (!resp.ok) {
    throw new Error(`加载示例素材失败(${resp.status}): ${url}`);
  }
  const dataUrl = await blobToDataUrl(await resp.blob());
  dataUrlCache.set(url, dataUrl);
  return dataUrl;
}
