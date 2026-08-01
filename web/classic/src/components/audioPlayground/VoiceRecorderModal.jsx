import React from 'react';
import { Modal, Button, Typography, Banner } from '@douyinfe/semi-ui';
import { Mic, Square, RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  useVoiceRecorder,
  isVoiceRecordSupported,
} from '../../hooks/audioPlayground/useVoiceRecorder';
import {
  VOICE_RECORD_SCRIPT,
  VOICE_RECORD_MIN_SEC,
  VOICE_RECORD_MAX_SEC,
} from '../../constants/audioPlayground.constants';

// 参考音色现场录制弹窗:给一段引导文案让用户照读,录完可试听/重录,确认后把 WAV
// data-url 交回给调用方(与「上传自定义音频」写同一个 inputs.voiceData)。
const VoiceRecorderModal = ({ visible, onClose, onConfirm }) => {
  const { t } = useTranslation();
  // preview 直接取 hook 的 result:手动停止与录满上限自动停止走同一条路。
  const { recording, seconds, error, result: preview, start, stop, reset } =
    useVoiceRecorder({ maxSeconds: VOICE_RECORD_MAX_SEC });

  const supported = isVoiceRecordSupported();

  const handleClose = () => {
    if (recording) stop();
    reset();
    onClose();
  };

  const handleConfirm = () => {
    if (!preview) return;
    onConfirm(preview.dataUrl, preview.duration);
    reset();
    onClose();
  };

  // 录太短音色特征不足,拦一下让用户重录。
  const tooShort = preview && preview.duration < VOICE_RECORD_MIN_SEC;

  return (
    <Modal
      title={t('录制参考音色')}
      visible={visible}
      onCancel={handleClose}
      footer={null}
      closeOnEsc={!recording}
      maskClosable={!recording}
      width={480}
    >
      {!supported && (
        <Banner
          type='warning'
          description={t(
            '当前浏览器不支持录音（需要 HTTPS 环境，部分内置浏览器亦不开放麦克风）。请改用上传音频文件。',
          )}
          closeIcon={null}
          className='mb-3'
        />
      )}

      {error && (
        <Banner
          type='danger'
          description={error}
          closeIcon={null}
          className='mb-3'
        />
      )}

      <Typography.Text type='tertiary' className='text-xs block mb-2'>
        {t('请在安静环境下用自然语气朗读以下文字，约 8-9 秒：')}
      </Typography.Text>
      <div className='p-3 rounded-lg bg-gray-50 text-sm leading-relaxed mb-4'>
        {VOICE_RECORD_SCRIPT}
      </div>

      {preview ? (
        <div>
          <audio
            src={preview.dataUrl}
            controls
            className='w-full'
            style={{ height: 36 }}
          />
          {tooShort && (
            <Typography.Text type='danger' className='text-xs block mt-2'>
              {t('录音过短（不足 {{sec}} 秒），建议重录', {
                sec: VOICE_RECORD_MIN_SEC,
              })}
            </Typography.Text>
          )}
          <div className='flex items-center gap-2 mt-4'>
            <Button
              theme='outline'
              type='tertiary'
              icon={<RotateCcw size={14} />}
              onClick={reset}
            >
              {t('重录')}
            </Button>
            <Button
              theme='solid'
              type='primary'
              onClick={handleConfirm}
              disabled={tooShort}
            >
              {t('使用这段录音')}
            </Button>
          </div>
        </div>
      ) : (
        <div className='flex items-center gap-3'>
          {recording ? (
            <>
              <Button
                theme='solid'
                type='danger'
                icon={<Square size={14} />}
                onClick={stop}
              >
                {t('停止')}
              </Button>
              <Typography.Text className='text-sm'>
                {t('录制中')} {seconds}s / {VOICE_RECORD_MAX_SEC}s
              </Typography.Text>
            </>
          ) : (
            <Button
              theme='solid'
              type='primary'
              icon={<Mic size={14} />}
              onClick={start}
              disabled={!supported}
            >
              {t('开始录制')}
            </Button>
          )}
        </div>
      )}
    </Modal>
  );
};

export default VoiceRecorderModal;
