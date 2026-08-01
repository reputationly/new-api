import { useCallback, useEffect, useRef, useState } from 'react';

// 参考音色现场录制。桌面端与移动端共用(只用浏览器原生 API,不引任何 UI 库,
// 避免移动端 semi-ui shim 传染)。
//
// 为什么录完要自己转 WAV:MediaRecorder 的产物格式随浏览器而变(Chrome/Android 是
// audio/webm;codecs=opus,Safari 是 audio/mp4)。而后端 nfsinput.extForData 的 MIME
// 白名单里没有 audio/webm —— 落不到白名单就回退成字段默认扩展名 .wav(见 nfsinput.go
// extForField),等于把 opus 数据存成 .wav 文件喂给引擎,必然解码失败。
// 这里统一解码成 PCM 再编 16bit WAV:两端格式一致、后端零改动,也正好落在 IndexTTS
// 建议的 16-48kHz WAV 上。不重采样(按 AudioContext 原采样率写),避免重采样失真。

// 单声道 16bit PCM WAV。参考音色不需要立体声,混单声道可省一半体积。
const encodeWav = (audioBuffer) => {
  const { numberOfChannels, sampleRate, length } = audioBuffer;
  const channels = [];
  for (let i = 0; i < numberOfChannels; i++) {
    channels.push(audioBuffer.getChannelData(i));
  }

  const buffer = new ArrayBuffer(44 + length * 2);
  const view = new DataView(buffer);
  const writeStr = (offset, str) => {
    for (let i = 0; i < str.length; i++) {
      view.setUint8(offset + i, str.charCodeAt(i));
    }
  };

  writeStr(0, 'RIFF');
  view.setUint32(4, 36 + length * 2, true);
  writeStr(8, 'WAVE');
  writeStr(12, 'fmt ');
  view.setUint32(16, 16, true); // PCM 子块大小
  view.setUint16(20, 1, true); // 格式:PCM
  view.setUint16(22, 1, true); // 声道数:单声道
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true); // 字节率 = 采样率 × 块对齐
  view.setUint16(32, 2, true); // 块对齐 = 声道数 × 位深/8
  view.setUint16(34, 16, true); // 位深
  writeStr(36, 'data');
  view.setUint32(40, length * 2, true);

  let offset = 44;
  for (let i = 0; i < length; i++) {
    let sample = 0;
    for (let c = 0; c < numberOfChannels; c++) sample += channels[c][i];
    sample /= numberOfChannels;
    const clamped = Math.max(-1, Math.min(1, sample));
    view.setInt16(offset, clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff, true);
    offset += 2;
  }

  return new Blob([buffer], { type: 'audio/wav' });
};

const blobToDataUrl = (blob) =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(new Error('读取录音失败'));
    reader.readAsDataURL(blob);
  });

// getUserMedia 只在安全上下文(https / localhost)可用;微信内置浏览器等 WebView 里
// 即便存在也可能被拒。supported 只作 UI 预判,真实可用性以 start() 的结果为准。
export const isVoiceRecordSupported = () =>
  typeof navigator !== 'undefined' &&
  !!navigator.mediaDevices?.getUserMedia &&
  typeof window !== 'undefined' &&
  typeof window.MediaRecorder !== 'undefined';

export const useVoiceRecorder = ({ maxSeconds = 20 } = {}) => {
  const [recording, setRecording] = useState(false);
  const [seconds, setSeconds] = useState(0);
  const [error, setError] = useState('');
  // 录音结果 { dataUrl, duration }。必须由 state 暴露而不是只从 stop() 返回:
  // 到达 maxSeconds 是内部自动停的,此时没有任何调用方在等 stop() 的返回值,
  // 结果只走返回值就会被丢掉(录满上限 = 白录)。两条停止路径都落到这里。
  const [result, setResult] = useState(null);

  const recorderRef = useRef(null);
  const streamRef = useRef(null);
  const chunksRef = useRef([]);
  const timerRef = useRef(null);
  const maxTimerRef = useRef(null);
  // 本次录制的代次。取消(关闭弹层)时调用方是 stop() + reset(),但 stop() 只是触发
  // MediaRecorder.stop(),onstop 里的解码是异步的 —— 等它跑完再 setResult,就会把用户
  // 刚取消掉的录音写回 state(组件常驻挂载,重开弹层就会看到它)。reset/卸载时递增代次,
  // 迟到的回调据此丢弃自己的结果。
  const genRef = useRef(0);

  // 释放麦克风:不显式 stop track,浏览器地址栏/系统的录音指示会一直亮着。
  const releaseStream = useCallback(() => {
    streamRef.current?.getTracks().forEach((tk) => tk.stop());
    streamRef.current = null;
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
    };
  }, [clearTimer, releaseStream]);

  const start = useCallback(async () => {
    setError('');
    if (!isVoiceRecordSupported()) {
      setError('当前浏览器不支持录音，请改用上传音频文件');
      return false;
    }
    // 代次必须在 await 之前占位:授权框挂起期间 recording 仍是 false,调用方的
    // handleClose 走 `if (recording) stop()` 拦不住,只会 reset()。若等授权回来才记代次,
    // 这段续段会在弹层已关闭的情况下照常开录,麦克风一直开到 maxTimer 才停;用户此间
    // 若重开弹层再点录制,streamRef/recorderRef 被覆盖,第一路 stream 失去引用永远
    // 释放不掉(指示灯常亮到刷新页面)。
    const gen = ++genRef.current;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      // 等授权期间已被关闭/重置:立即释放,不要开录。
      if (genRef.current !== gen) {
        stream.getTracks().forEach((tk) => tk.stop());
        return false;
      }
      streamRef.current = stream;
      chunksRef.current = [];
      // 不指定 mimeType:各浏览器支持的容器不同,交给浏览器选,反正最终统一转 WAV。
      const recorder = new MediaRecorder(stream);
      recorderRef.current = recorder;
      recorder.ondataavailable = (e) => {
        if (e.data?.size > 0) chunksRef.current.push(e.data);
      };
      recorder.onstop = async () => {
        clearTimer();
        releaseStream();
        // 录制确实已停,recording 无条件复位,否则重开弹层会卡在「录制中」。
        setRecording(false);
        try {
          const raw = new Blob(chunksRef.current, {
            type: chunksRef.current[0]?.type || 'audio/webm',
          });
          const AudioCtx = window.AudioContext || window.webkitAudioContext;
          const ctx = new AudioCtx();
          const decoded = await ctx.decodeAudioData(await raw.arrayBuffer());
          const wav = encodeWav(decoded);
          ctx.close();
          const dataUrl = await blobToDataUrl(wav);
          // 已被取消/重置:这段录音用户不要了,丢弃而不是写回 state。
          if (genRef.current !== gen) return;
          setResult({ dataUrl, duration: decoded.duration });
        } catch (e) {
          if (genRef.current !== gen) return;
          setError('录音处理失败，请重试或改用上传音频文件');
        }
      };
      recorder.start();
      setRecording(true);
      setSeconds(0);
      setResult(null);
      // 计时只负责显示秒数,不在 setState updater 里做副作用。
      timerRef.current = setInterval(() => setSeconds((prev) => prev + 1), 1000);
      // 硬上限:到点自动停,结果照常经 onstop 落进 result。
      maxTimerRef.current = setTimeout(() => {
        if (recorderRef.current?.state === 'recording') {
          recorderRef.current.stop();
        }
      }, maxSeconds * 1000);
      return true;
    } catch (e) {
      releaseStream();
      // 已关闭/重置:别把陈旧错误留到下次打开。
      if (genRef.current !== gen) return false;
      // NotAllowedError = 用户拒绝或被 WebView 策略拦下,是最常见的一种。
      setError(
        e?.name === 'NotAllowedError'
          ? '麦克风权限被拒绝，请在浏览器设置中允许后重试，或改用上传音频文件'
          : '无法访问麦克风，请改用上传音频文件',
      );
      return false;
    }
  }, [clearTimer, maxSeconds, releaseStream]);

  // 停止录制。结果异步落进 result(转码需要时间),不从这里返回。
  const stop = useCallback(() => {
    if (recorderRef.current?.state === 'recording') recorderRef.current.stop();
  }, []);

  const reset = useCallback(() => {
    // 作废进行中/待解码的那次录制,避免它的结果稍后覆盖这次清空。
    genRef.current++;
    setSeconds(0);
    setError('');
    setResult(null);
  }, []);

  return { recording, seconds, error, result, start, stop, reset };
};
