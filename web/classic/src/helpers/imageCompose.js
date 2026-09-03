// 把一张图重排成指定宽高比的新图（浏览器 canvas，纯客户端，不占服务端资源）。
//
// 存在的理由：关键帧类玩法的输出画幅由**上传的那张图**决定（引擎按 images[0] 的
// 宽高比推画布，传来的 aspect_ratio 被静默忽略）。所以「让用户选画幅」这件事，唯一
// 不需要动引擎、也不会把画面弄坏的做法，就是在提交前把图本身改成目标比例——发出去的
// 请求体一个字段都不用变。
//
// 2026-08-24 在现网 H3 实例(MiniMax-H3-FL2VA-Turbo8)上实测过三种做法，结论是只有
// 「改图」这一条走得通：
//   - 显式下发 width/height 盖画布：引擎把图**拉伸**到新画布(keyframes.py 的
//     index==0 分支两种合同都走 resize)，9:16 的图要 16:9 会横向拉开三倍多，圆变椭圆。
//   - 贴白边交给模型补：模型没有 outpaint 能力。合成图上白边会在约 2 秒内被"消融"成
//     模型自由发挥的内容；真实照片上则是**镜头一路推近**直到白边被吃光，构图完全失控，
//     且开头约 1 秒仍带着白边。
//   - 虚化补边：模糊边**全程稳定保留**，主体不动、不裁切、不形变——模型把它当成合法的
//     「竖屏内容 + 虚化背景」形态（短视频里常见），不去改它。故 FIT_BLUR 可用。
//
// 两种模式各自的取舍：
//   FIT_CROP 构图干净但丢内容（9:16 → 16:9 只剩中间约 32% 的高度）
//   FIT_BLUR 保内容但成品是虚化边框风格
export const FIT_CROP = 'crop';
export const FIT_BLUR = 'blur';

// 合成图的最大边。限体积（提交体是 base64，且运营配的 maxInputMB 是按用户原图大小拍的，
// 没算过合成后会变大）；同时远低于引擎对参考图 5760 的上限，不会撞到硬校验。
const MAX_EDGE = 1920;
// 引擎对参考图的短边下限是 256，比它小直接 400。合成时按它兜底。
const MIN_EDGE = 256;
// 虚化背景的模糊半径（按画布短边的比例给，小图上才不会糊成一坨纯色）。
const BLUR_RATIO = 0.04;
// 原图比例与目标比例差在此以内就原样返回：重编码一次只会掉画质，换不来任何东西。
const RATIO_EPS = 0.01;

// "16:9" → 1.777…。解析不出返回 0，调用方据此跳过合成。
export const parseRatio = (raw) => {
  const m = /^\s*(\d+(?:\.\d+)?)\s*[:x×]\s*(\d+(?:\.\d+)?)\s*$/.exec(
    String(raw || ''),
  );
  if (!m) return 0;
  const w = Number(m[1]);
  const h = Number(m[2]);
  if (!(w > 0) || !(h > 0)) return 0;
  return w / h;
};

const loadImage = (dataUrl) =>
  new Promise((resolve) => {
    const img = new window.Image();
    img.onload = () =>
      resolve(img.naturalWidth && img.naturalHeight ? img : null);
    // 解不出就返回 null，调用方原样提交那张图：宁可让画幅跟随原图，也不要因为浏览器
    // 解不了某种格式就把整条提交拦下来。
    img.onerror = () => resolve(null);
    img.src = dataUrl;
  });

/**
 * 读出一张图的真实宽高比（w/h）。解不出返回 0，调用方据此跳过下发。
 *
 * 只有「画布必须由请求给出」的那类引擎需要它：LTX-2.5 的 i2v 是引擎按请求里的
 * width/height 把首帧等比放大到覆盖后居中裁剪，不像 H3/wan 那样按 images[0] 推画布。
 * 于是用户选「跟随上传素材」时，既没有具名比例、又必须有一个画布——把图的真实比例
 * 交给网关去合成，是唯一能让这个档在 LTX 上落地的做法。见 useVideoGeneration 提交处
 * 与后端 ltx25.go 的 ltx25SourceRatioKey。
 *
 * 只回比例、不回像素：档位词→短边的映射（1080P 的短边是 1088 不是 1080）与对齐粒度
 * （一阶段 32、两阶段 64）都在网关侧，前端再算一份必然分叉，而分叉的症状是出片尺寸
 * 不对、只有量像素才看得出来。
 */
export const imageAspectRatio = async (dataUrl) => {
  if (!dataUrl) return 0;
  const img = await loadImage(dataUrl);
  if (!img) return 0;
  return img.naturalWidth / img.naturalHeight;
};

// 目标画布尺寸：保持与原图相近的面积（不无端放大或缩小画质），按目标比例摆开，
// 再钳进 [MIN_EDGE, MAX_EDGE]。
const canvasSize = (srcW, srcH, ratio) => {
  const area = srcW * srcH;
  let w = Math.round(Math.sqrt(area * ratio));
  let h = Math.round(Math.sqrt(area / ratio));
  const shrink = MAX_EDGE / Math.max(w, h);
  if (shrink < 1) {
    w = Math.round(w * shrink);
    h = Math.round(h * shrink);
  }
  const grow = MIN_EDGE / Math.min(w, h);
  if (grow > 1) {
    w = Math.round(w * grow);
    h = Math.round(h * grow);
  }
  return [Math.max(1, w), Math.max(1, h)];
};

/**
 * 把 dataUrl 重排成 ratio 指定的比例。
 *
 * @param {string} dataUrl 原图（体验区的上传槽存的就是 data URL，同源，canvas 不会被
 *   跨域污染。若将来支持粘贴外链图片，这里要先经 /pg/images/proxy 中转，否则
 *   toDataURL 抛 SecurityError）。
 * @param {string} ratio 目标比例，如 '16:9'
 * @param {string} mode FIT_CROP | FIT_BLUR
 * @returns {Promise<string>} 新的 data URL；任何一步不成立都原样返回入参，
 *   让画幅回落到「跟随原图」——那是个永远安全的结果。
 */
export const composeImageToRatio = async (dataUrl, ratio, mode) => {
  const target = parseRatio(ratio);
  if (!dataUrl || !target) return dataUrl;
  const img = await loadImage(dataUrl);
  if (!img) return dataUrl;

  const srcW = img.naturalWidth;
  const srcH = img.naturalHeight;
  if (Math.abs(srcW / srcH - target) / target <= RATIO_EPS) return dataUrl;

  const [w, h] = canvasSize(srcW, srcH, target);
  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext('2d');
  if (!ctx) return dataUrl;

  if (mode === FIT_CROP) {
    // cover：等比放大到盖满画布，多出来的居中裁掉。
    const scale = Math.max(w / srcW, h / srcH);
    const dw = srcW * scale;
    const dh = srcH * scale;
    ctx.drawImage(img, (w - dw) / 2, (h - dh) / 2, dw, dh);
  } else {
    // 背景：同样 cover 铺满，再高斯模糊——它只是用来填两侧空白的墙纸。
    const bgScale = Math.max(w / srcW, h / srcH);
    const bw = srcW * bgScale;
    const bh = srcH * bgScale;
    ctx.filter = `blur(${Math.max(4, Math.round(Math.min(w, h) * BLUR_RATIO))}px)`;
    ctx.drawImage(img, (w - bw) / 2, (h - bh) / 2, bw, bh);
    // 模糊在边缘会把画布外的透明像素卷进来，露出一圈发白的边。再画一遍放大一点的
    // 背景盖住它，比调 shadow/clip 都省事。
    ctx.drawImage(
      img,
      (w - bw * 1.1) / 2,
      (h - bh * 1.1) / 2,
      bw * 1.1,
      bh * 1.1,
    );
    ctx.filter = 'none';
    // 前景：contain 居中，原图一个像素都不裁、比例不变。
    const fgScale = Math.min(w / srcW, h / srcH);
    const fw = srcW * fgScale;
    const fh = srcH * fgScale;
    ctx.drawImage(img, (w - fw) / 2, (h - fh) / 2, fw, fh);
  }

  // JPEG 而非 PNG：合成图是满画幅的照片内容，PNG 会把提交体撑到几 MB，撞运营按
  // 原图大小拍的 maxInputMB 闸门。0.9 在这个尺寸上肉眼无损。
  try {
    return canvas.toDataURL('image/jpeg', 0.9);
  } catch (e) {
    return dataUrl;
  }
};
