import React, { useEffect, useRef, useState } from 'react';
import { Modal, Button, Typography, Banner } from '@douyinfe/semi-ui';
import { Video, Square, RotateCcw, SwitchCamera } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  useVideoRecorder,
  isVideoRecordSupported,
} from '../../hooks/videoPlayground/useVideoRecorder';
import {
  VIDEO_RECORD_HEIGHT,
  VIDEO_RECORD_FPS,
  VIDEO_RECORD_MAX_SEC,
} from '../../constants/videoPlayground.constants';

// 现场拍摄弹窗:替掉手机上唤起系统相机那条路(系统相机的档位网页管不着,华为一律 4K30,
// 十几秒就顶穿上传上限)。这里自己取景 + 录制,档位由 useVideoRecorder 定死,录完可回放/
// 重录,确认后把 data-url 交回调用方(与「上传视频文件」写同一个字段)。
const VideoRecorderModal = ({ visible, onClose, onConfirm, maxMB = 0 }) => {
  const { t } = useTranslation();
  const {
    recording,
    seconds,
    error,
    stream,
    facingMode,
    result: preview,
    openCamera,
    closeCamera,
    start,
    stop,
    reset,
    toDataUrl,
  } = useVideoRecorder({ maxSeconds: VIDEO_RECORD_MAX_SEC });

  const liveRef = useRef(null);
  const [converting, setConverting] = useState(false);

  const supported = isVideoRecordSupported();

  // 打开即取景:视频不像录音,没有画面用户无法构图,所以提前要权限而不是等按下录制。
  useEffect(() => {
    if (visible && supported) openCamera();
    // 关闭由 handleClose 负责(要区分「确认」与「取消」),这里只管开。
  }, [visible, supported, openCamera]);

  // srcObject 只能用命令式赋值,JSX 属性传不了 MediaStream。
  useEffect(() => {
    const el = liveRef.current;
    if (!el) return;
    el.srcObject = stream || null;
    if (stream) el.play?.().catch(() => {});
  }, [stream]);

  const sizeMB = preview ? preview.size / 1024 / 1024 : 0;
  // 上传上限是调用方给的(maxInputMB),超了就别让用户白提交一次。
  const oversized = maxMB > 0 && sizeMB > maxMB;

  const handleClose = () => {
    reset();
    closeCamera();
    onClose();
  };

  const handleConfirm = async () => {
    if (!preview || oversized) return;
    setConverting(true);
    try {
      const dataUrl = await toDataUrl();
      if (!dataUrl) return;
      onConfirm(dataUrl);
      reset();
      closeCamera();
      onClose();
    } finally {
      setConverting(false);
    }
  };

  // 重录:丢掉这段,摄像头留着(reset 不关流),省一次授权往返。
  const handleRetake = () => {
    reset();
    if (!stream) openCamera();
  };

  return (
    <Modal
      title={t('现场拍摄')}
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
            '当前浏览器不支持录像（需要 HTTPS 环境，部分内置浏览器亦不开放摄像头）。请改用上传视频文件。',
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
        {t('按 {{height}}P / {{fps}}fps 录制，比手机系统相机的 4K 小很多，最长 {{sec}} 秒', {
          height: VIDEO_RECORD_HEIGHT,
          fps: VIDEO_RECORD_FPS,
          sec: VIDEO_RECORD_MAX_SEC,
        })}
      </Typography.Text>

      {/* 录完展示回放,未录完展示实时取景。两者都固定 16:9 黑底,避免切换时弹层跳高。 */}
      <div className='rounded-lg overflow-hidden bg-black mb-3' style={{ aspectRatio: '16 / 9' }}>
        {preview ? (
          <video
            src={preview.url}
            controls
            playsInline
            className='w-full h-full'
            style={{ objectFit: 'contain' }}
          />
        ) : (
          <video
            ref={liveRef}
            muted
            playsInline
            autoPlay
            className='w-full h-full'
            style={{ objectFit: 'contain' }}
          />
        )}
      </div>

      {preview ? (
        <div>
          <Typography.Text type='tertiary' className='text-xs block'>
            {t('时长 {{sec}} 秒，大小 {{size}} MB', {
              sec: preview.seconds.toFixed(1),
              size: sizeMB.toFixed(1),
            })}
          </Typography.Text>
          {oversized && (
            <Typography.Text type='danger' className='text-xs block mt-2'>
              {t('文件超过 {{size}} MB 上限，请缩短时长后重录', { size: maxMB })}
            </Typography.Text>
          )}
          <div className='flex items-center gap-2 mt-4'>
            <Button
              theme='outline'
              type='tertiary'
              icon={<RotateCcw size={14} />}
              onClick={handleRetake}
            >
              {t('重拍')}
            </Button>
            <Button
              theme='solid'
              type='primary'
              onClick={handleConfirm}
              loading={converting}
              disabled={oversized}
            >
              {t('使用这段录像')}
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
                {t('录制中')} {seconds}s / {VIDEO_RECORD_MAX_SEC}s
              </Typography.Text>
            </>
          ) : (
            <>
              <Button
                theme='solid'
                type='primary'
                icon={<Video size={14} />}
                onClick={start}
                disabled={!supported || !stream}
              >
                {t('开始录制')}
              </Button>
              {/* 切前后摄只在未录制时给:同一条 stream 改不了朝向,得关掉重开。 */}
              <Button
                theme='borderless'
                type='tertiary'
                icon={<SwitchCamera size={14} />}
                onClick={() =>
                  openCamera(facingMode === 'environment' ? 'user' : 'environment')
                }
                disabled={!supported || !stream}
              >
                {t('切换摄像头')}
              </Button>
            </>
          )}
        </div>
      )}
    </Modal>
  );
};

export default VideoRecorderModal;
