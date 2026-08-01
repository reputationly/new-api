import React from 'react';
import { Button, Popup } from 'antd-mobile';

import {
  useVoiceRecorder,
  isVoiceRecordSupported,
} from '@classic/hooks/audioPlayground/useVoiceRecorder';
import {
  VOICE_RECORD_SCRIPT,
  VOICE_RECORD_MIN_SEC,
  VOICE_RECORD_MAX_SEC,
} from '@classic/constants/audioPlayground.constants';

// 参考音色现场录制(移动端)。录音与转 WAV 的逻辑与桌面端共用同一个 hook,
// 这里只是 antd-mobile 的壳。
const VoiceRecorder = ({ visible, onClose, onConfirm }) => {
  // preview 直接取 hook 的 result:手动停止与录满上限自动停止走同一条路。
  const { recording, seconds, error, result: preview, start, stop, reset } =
    useVoiceRecorder({ maxSeconds: VOICE_RECORD_MAX_SEC });

  const supported = isVoiceRecordSupported();
  const tooShort = preview && preview.duration < VOICE_RECORD_MIN_SEC;

  const handleClose = () => {
    if (recording) stop();
    reset();
    onClose();
  };

  const handleConfirm = () => {
    if (!preview) return;
    onConfirm(preview.dataUrl);
    reset();
    onClose();
  };

  return (
    <Popup
      visible={visible}
      onMaskClick={recording ? undefined : handleClose}
      onClose={handleClose}
      bodyStyle={{
        padding: 16,
        borderTopLeftRadius: 12,
        borderTopRightRadius: 12,
      }}
    >
      <div style={{ fontSize: 16, fontWeight: 500, marginBottom: 12 }}>
        录制参考音色
      </div>

      {!supported && (
        <div
          style={{
            padding: '8px 12px',
            borderRadius: 8,
            background: '#fffbeb',
            color: '#b45309',
            fontSize: 13,
            marginBottom: 12,
          }}
        >
          当前浏览器不支持录音（微信等内置浏览器可能不开放麦克风），请改用上传音频文件。
        </div>
      )}

      {error && (
        <div
          style={{
            padding: '8px 12px',
            borderRadius: 8,
            background: '#fef2f2',
            color: 'var(--adm-color-danger)',
            fontSize: 13,
            marginBottom: 12,
          }}
        >
          {error}
        </div>
      )}

      <div
        style={{ fontSize: 12, color: 'var(--adm-color-weak)', marginBottom: 6 }}
      >
        请在安静环境下用自然语气朗读以下文字，约 8-9 秒：
      </div>
      <div
        style={{
          padding: 12,
          borderRadius: 8,
          background: 'var(--adm-color-fill-content, #f5f5f5)',
          fontSize: 15,
          lineHeight: 1.7,
          marginBottom: 16,
        }}
      >
        {VOICE_RECORD_SCRIPT}
      </div>

      {preview ? (
        <div>
          <audio src={preview.dataUrl} controls style={{ width: '100%' }} />
          {tooShort && (
            <div
              style={{
                color: 'var(--adm-color-danger)',
                fontSize: 12,
                marginTop: 6,
              }}
            >
              录音过短（不足 {VOICE_RECORD_MIN_SEC} 秒），建议重录
            </div>
          )}
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <Button
              size='small'
              fill='outline'
              onClick={reset}
            >
              重录
            </Button>
            <Button
              size='small'
              color='primary'
              disabled={tooShort}
              onClick={handleConfirm}
            >
              使用这段录音
            </Button>
          </div>
        </div>
      ) : (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {recording ? (
            <>
              <Button size='small' color='danger' onClick={stop}>
                停止
              </Button>
              <span style={{ fontSize: 14 }}>
                录制中 {seconds}s / {VOICE_RECORD_MAX_SEC}s
              </span>
            </>
          ) : (
            <Button
              size='small'
              color='primary'
              disabled={!supported}
              onClick={start}
            >
              开始录制
            </Button>
          )}
        </div>
      )}
    </Popup>
  );
};

export default VoiceRecorder;
