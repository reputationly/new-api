import { useCallback, useEffect, useRef, useState } from 'react';

import {
  VIDEO_RECORD_WIDTH,
  VIDEO_RECORD_HEIGHT,
  VIDEO_RECORD_FPS,
  VIDEO_RECORD_VIDEO_BPS,
  VIDEO_RECORD_AUDIO_BPS,
  VIDEO_RECORD_MAX_SEC,
} from '../../constants/videoPlayground.constants';

// 视频现场录制。桌面端与移动端共用(只用浏览器原生 API,不引任何 UI 库,与
// useVoiceRecorder 同一路数)。
//
// 为什么要自己录:手机上点 <input type="file" accept="video/*"> 唤起的是系统相机,网页
// 对它的分辨率/码率没有任何控制权 —— Android 走 ACTION_VIDEO_CAPTURE intent,虽然该
// intent 本身有 EXTRA_VIDEO_QUALITY/EXTRA_SIZE_LIMIT,但浏览器不会替网页传,也没暴露
// 任何 API 让网页设置。实测部分 ROM(EMUI/HarmonyOS)在 intent 模式下连相机 app 里存的
// 档位都不认,一律按最高档录:华为 4K30 约 5-7 MB/s,录十几秒就顶穿 maxInputMB。
// 改走 getUserMedia + MediaRecorder,档位由代码定死,与用户手机设置无关。
//
// 产物格式:优先 mp4(H.264),浏览器不支持才退 webm。不像参考音那样需要前端转码 ——
// 后端 nfsinput 两者都收(extForData 有 video/mp4 与 video/webm,isVideoBytes 认 ISO
// BMFF 的 ftyp 与 webm 的 EBML 魔数),MediaRecorder 的产物直接交上去即可。

const MIME_CANDIDATES = [
  'video/mp4;codecs=avc1.42E01E,mp4a.40.2',
  'video/mp4',
  'video/webm;codecs=vp9,opus',
  'video/webm;codecs=vp8,opus',
  'video/webm',
];

// 返回 '' 表示一个都探不到,此时不给 MediaRecorder 指定 mimeType,让浏览器自选。
const pickMimeType = () => {
  if (typeof window === 'undefined' || !window.MediaRecorder) return '';
  for (const type of MIME_CANDIDATES) {
    if (window.MediaRecorder.isTypeSupported?.(type)) return type;
  }
  return '';
};

const blobToDataUrl = (blob) =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(new Error('读取录像失败'));
    reader.readAsDataURL(blob);
  });

// getUserMedia 只在安全上下文(https / localhost)可用;微信内置浏览器等 WebView 里
// 即便存在也可能被拒。supported 只作 UI 预判,真实可用性以 openCamera() 的结果为准。
export const isVideoRecordSupported = () =>
  typeof navigator !== 'undefined' &&
  !!navigator.mediaDevices?.getUserMedia &&
  typeof window !== 'undefined' &&
  typeof window.MediaRecorder !== 'undefined';

export const useVideoRecorder = ({ maxSeconds = VIDEO_RECORD_MAX_SEC } = {}) => {
  const [recording, setRecording] = useState(false);
  const [seconds, setSeconds] = useState(0);
  const [error, setError] = useState('');
  // 取景用的实时流。视频不同于录音:不给预览用户没法构图,所以弹层一打开就开摄像头。
  const [stream, setStream] = useState(null);
  const [facingMode, setFacingMode] = useState('environment');
  // 录制结果 { url, blob, seconds, size, mimeType }。必须由 state 暴露而不是只从 stop()
  // 返回:到达 maxSeconds 是内部自动停的,此时没有任何调用方在等 stop() 的返回值。
  // url 是 objectURL(不是 data-url):给弹层里的回放用,避免用户还在预览/重录阶段就把
  // 几十 MB 的 base64 字符串常驻内存,真正转 data-url 推迟到 confirm。
  const [result, setResult] = useState(null);

  const recorderRef = useRef(null);
  const streamRef = useRef(null);
  const chunksRef = useRef([]);
  const timerRef = useRef(null);
  const maxTimerRef = useRef(null);
  const startedAtRef = useRef(0);
  const resultUrlRef = useRef('');
  // 当前朝向另存一份 ref:openCamera 若从 state 读 facingMode,它的函数标识会随切换而变,
  // 弹层里「打开即取景」的 effect 就会被重复触发(切一次前后摄 = 多开一次摄像头)。
  const facingRef = useRef('environment');
  // 本次会话的代次。与 useVoiceRecorder 同理:授权/编码都是异步的,关闭弹层后迟到的回调
  // 不能再把流或结果写回 state,否则摄像头指示灯常亮、或者用户已取消的片段又冒出来。
  const genRef = useRef(0);

  // 释放摄像头+麦克风:不显式 stop track,系统的录制指示会一直亮着。
  const releaseStream = useCallback(() => {
    streamRef.current?.getTracks().forEach((tk) => tk.stop());
    streamRef.current = null;
    setStream(null);
  }, []);

  const revokeResultUrl = useCallback(() => {
    if (resultUrlRef.current) URL.revokeObjectURL(resultUrlRef.current);
    resultUrlRef.current = '';
  }, []);

  const clearTimer = useCallback(() => {
    if (timerRef.current) clearInterval(timerRef.current);
    timerRef.current = null;
    if (maxTimerRef.current) clearTimeout(maxTimerRef.current);
    maxTimerRef.current = null;
  }, []);

  useEffect(() => {
    return () => {
      genRef.current++; // 卸载后迟到的 onstop 不再 setState
      clearTimer();
      if (recorderRef.current?.state === 'recording') {
        recorderRef.current.stop();
      }
      releaseStream();
      revokeResultUrl();
    };
  }, [clearTimer, releaseStream, revokeResultUrl]);

  // 开摄像头取景。facing 传入时同时切换前后摄(切换 = 关掉重开,同一 stream 改不了)。
  const openCamera = useCallback(
    async (facing) => {
      const target = facing || facingRef.current;
      setError('');
      if (!isVideoRecordSupported()) {
        setError('当前浏览器不支持录像，请改用上传视频文件');
        return false;
      }
      // 代次在 await 之前占位,理由同 useVoiceRecorder:授权框挂起期间弹层可能已被关闭,
      // 等授权回来才记代次的话,这路 stream 会失去引用永远释放不掉。
      const gen = ++genRef.current;
      // 先释放旧流:切摄像头时不放手,部分机型会直接拒绝第二次 getUserMedia。
      releaseStream();
      try {
        const next = await navigator.mediaDevices.getUserMedia({
          // 一律用 ideal 而非 exact:exact 在不支持该档位的设备上直接抛
          // OverconstrainedError,ideal 会退到最接近的档位,总比录不了强。
          video: {
            width: { ideal: VIDEO_RECORD_WIDTH },
            height: { ideal: VIDEO_RECORD_HEIGHT },
            frameRate: { ideal: VIDEO_RECORD_FPS },
            facingMode: { ideal: target },
          },
          audio: true,
        });
        if (genRef.current !== gen) {
          next.getTracks().forEach((tk) => tk.stop());
          return false;
        }
        streamRef.current = next;
        setStream(next);
        facingRef.current = target;
        setFacingMode(target);
        return true;
      } catch (e) {
        if (genRef.current !== gen) return false;
        // NotAllowedError = 用户拒绝或被 WebView 策略拦下,是最常见的一种。
        setError(
          e?.name === 'NotAllowedError'
            ? '摄像头权限被拒绝，请在浏览器设置中允许后重试，或改用上传视频文件'
            : '无法访问摄像头，请改用上传视频文件',
        );
        return false;
      }
    },
    [releaseStream],
  );

  const closeCamera = useCallback(() => {
    genRef.current++;
    clearTimer();
    if (recorderRef.current?.state === 'recording') recorderRef.current.stop();
    releaseStream();
    setRecording(false);
    setSeconds(0);
  }, [clearTimer, releaseStream]);

  const start = useCallback(() => {
    const live = streamRef.current;
    if (!live) {
      setError('摄像头未就绪，请重试');
      return false;
    }
    const gen = genRef.current;
    try {
      chunksRef.current = [];
      const mimeType = pickMimeType();
      // 码率是「小文件」的真正开关:只压分辨率不压码率,编码器仍会按默认高码率写。
      const recorder = new window.MediaRecorder(live, {
        ...(mimeType ? { mimeType } : {}),
        videoBitsPerSecond: VIDEO_RECORD_VIDEO_BPS,
        audioBitsPerSecond: VIDEO_RECORD_AUDIO_BPS,
      });
      recorderRef.current = recorder;
      recorder.ondataavailable = (e) => {
        if (e.data?.size > 0) chunksRef.current.push(e.data);
      };
      recorder.onstop = () => {
        clearTimer();
        // 录制确实已停,recording 无条件复位,否则重开弹层会卡在「录制中」。
        setRecording(false);
        // 停止录制不关摄像头:用户可能要接着重录,留着取景省一次授权往返。
        if (genRef.current !== gen) return;
        const type = chunksRef.current[0]?.type || mimeType || 'video/webm';
        const blob = new Blob(chunksRef.current, { type });
        chunksRef.current = [];
        if (!blob.size) {
          setError('录像为空，请重试');
          return;
        }
        // MediaRecorder 的 webm 产物头部常常不写时长,读 video.duration 会拿到
        // Infinity。这里直接用墙钟时间,够用且不需要额外解析容器。
        const elapsed = (Date.now() - startedAtRef.current) / 1000;
        revokeResultUrl();
        const url = URL.createObjectURL(blob);
        resultUrlRef.current = url;
        setResult({
          url,
          blob,
          seconds: elapsed,
          size: blob.size,
          mimeType: type,
        });
      };
      // timeslice 1s:不切片的话整段编码只在 stop 时吐一个 Blob,长录制期间内存一直涨。
      recorder.start(1000);
      startedAtRef.current = Date.now();
      setRecording(true);
      setSeconds(0);
      setError('');
      revokeResultUrl();
      setResult(null);
      timerRef.current = setInterval(() => setSeconds((prev) => prev + 1), 1000);
      // 硬上限:到点自动停,结果照常经 onstop 落进 result。
      maxTimerRef.current = setTimeout(() => {
        if (recorderRef.current?.state === 'recording') {
          recorderRef.current.stop();
        }
      }, maxSeconds * 1000);
      return true;
    } catch (e) {
      clearTimer();
      setRecording(false);
      setError('无法开始录像，请改用上传视频文件');
      return false;
    }
  }, [clearTimer, maxSeconds, revokeResultUrl]);

  // 停止录制。结果异步落进 result,不从这里返回。
  const stop = useCallback(() => {
    if (recorderRef.current?.state === 'recording') recorderRef.current.stop();
  }, []);

  // 丢弃当前片段(重录/取消)。摄像头由调用方决定关不关。
  const reset = useCallback(() => {
    genRef.current++;
    clearTimer();
    if (recorderRef.current?.state === 'recording') recorderRef.current.stop();
    setRecording(false);
    setSeconds(0);
    setError('');
    revokeResultUrl();
    setResult(null);
  }, [clearTimer, revokeResultUrl]);

  // 交付给调用方时才转 data-url —— 与既有上传路径(readAsDataURL)保持同一种载荷。
  const toDataUrl = useCallback(async () => {
    if (!result?.blob) return '';
    return blobToDataUrl(result.blob);
  }, [result]);

  return {
    recording,
    seconds,
    error,
    stream,
    facingMode,
    result,
    openCamera,
    closeCamera,
    start,
    stop,
    reset,
    toDataUrl,
  };
};
