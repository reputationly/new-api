import React, { useRef, useState } from 'react';
import { Button, Typography, Modal } from '@douyinfe/semi-ui';
import { IconUpload } from '@douyinfe/semi-icons';
import { X, RefreshCw, Play, Video } from 'lucide-react';
import { useDropzone } from 'react-dropzone';
import { useTranslation } from 'react-i18next';
import { showError } from '../../helpers';
import VideoRecorderModal from './VideoRecorderModal';
import { isVideoRecordSupported } from '../../hooks/videoPlayground/useVideoRecorder';
import { AUDIO_DURATION_TOLERANCE_SEC } from '../../constants/videoPlayground.constants';

// 探测媒体时长(秒)。拿不到就 resolve(null) —— 浏览器解不了的容器不该因此挡住上传,
// 与后端 nfsinput.checkAudioDuration「解析失败即放行」同策略。
//
// 元素按 kind 建:虽然 <audio> 多数情况下也能读出视频容器的 duration,但那是在赌浏览器
// 的解复用实现,换个编码就 resolve(null) 静默失去这道闸。<video> 是它该用的元素。
const probeMediaSeconds = (file, kind) =>
  new Promise((resolve) => {
    const url = URL.createObjectURL(file);
    const el = document.createElement(kind === 'video' ? 'video' : 'audio');
    let settled = false;
    const done = (sec) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      URL.revokeObjectURL(url);
      resolve(sec);
    };
    // 兜底超时:某些容器两个事件都不触发,没有它整个选择流程会静默卡死(既不报错也不上传),
    // 是比漏检更糟的体验。超时按「探测不出来」处理,交给后端物化时兜底。
    const timer = setTimeout(() => done(null), 5000);
    el.preload = 'metadata';
    el.onloadedmetadata = () =>
      done(Number.isFinite(el.duration) ? el.duration : null);
    el.onerror = () => done(null);
    el.src = url;
  });

// 音频/视频单文件上传:读成 base64 data-url 交给上层(new-api 侧渠道会物化到 NFS,与
// 图生视频的帧图同机制)。支持点击选择与拖拽上传,带体积上限 + 试听/预览。
// kind: 'audio' | 'video'。
// maxSec: 时长上限(秒;0=不限),音频与视频同用。与 maxMB 正交——体积挡不住时长
//   (低码率的长视频体积可以很小),两道闸缺一不可。
const MediaFileInput = ({
  label,
  required = false,
  kind = 'audio',
  value = '', // base64 data-url 或 ''
  accept,
  maxMB = 50,
  maxSec = 0,
  disabled = false,
  onChange,
}) => {
  const { t } = useTranslation();
  const inputRef = useRef(null);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [recorderOpen, setRecorderOpen] = useState(false);

  // 现场拍摄只给视频槽。手机上走 <input type="file"> 唤起的是系统相机,分辨率/码率网页
  // 完全管不着(华为一律 4K30,十几秒就顶穿 maxMB),自己录才能把档位定死。
  const canRecord = kind === 'video' && isVideoRecordSupported();

  const defaultAccept =
    kind === 'video' ? 'video/*,.mp4,.mov,.webm' : 'audio/*,.wav,.mp3,.m4a';

  const readFile = async (file) => {
    if (!file) return;
    if (maxMB > 0 && file.size > maxMB * 1024 * 1024) {
      showError(t('文件不能超过 {{size}} MB', { size: maxMB }));
      return;
    }
    // 时长闸:数字人的输出时长 = min(驱动音频时长, video_duration, 参考视频时长),
    // 音频越长生成越久,过长会让引擎 OOM 或长时间占卡。选完文件当场拦,不等提交
    // (后端物化时另有同名兜底,防直连绕过)。
    //
    // 容差必须与后端同值,见 AUDIO_DURATION_TOLERANCE_SEC:这边更严就会出现"后端放行了、
    // 界面却不让选"——用户眼里的一分钟音频常是 60.024 秒,卡死整数会把它当场弹回。
    //
    // 视频同样走这道闸(参考生视频的单段参考视频有时长上限):容差沿用同一个常量,
    // 理由一样——真实时长几乎从不是整数,卡死整数会把用户眼里合法的素材当场弹回。
    if (maxSec > 0) {
      const sec = await probeMediaSeconds(file, kind);
      if (sec != null && sec > maxSec + AUDIO_DURATION_TOLERANCE_SEC) {
        showError(
          kind === 'video'
            ? t('视频时长 {{sec}} 秒，超过上限 {{max}} 秒', {
                sec: sec.toFixed(2),
                max: maxSec,
              })
            : t('音频时长 {{sec}} 秒，超过上限 {{max}} 秒', {
                sec: sec.toFixed(2),
                max: maxSec,
              }),
        );
        return;
      }
    }
    const reader = new FileReader();
    reader.onload = () => onChange(reader.result);
    reader.onerror = () => showError(t('读取文件失败'));
    reader.readAsDataURL(file);
  };

  const handleFile = (e) => {
    const file = e.target.files?.[0];
    e.target.value = ''; // 允许再次选同一文件
    readFile(file);
  };

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    accept: kind === 'video' ? { 'video/*': [] } : { 'audio/*': [] },
    multiple: false,
    disabled,
    noClick: true,
    onDrop: (files) => readFile(files?.[0]),
  });

  const idleText =
    kind === 'video'
      ? t('拖拽视频到此处，或点击选择文件')
      : t('拖拽音频到此处，或点击选择文件');

  return (
    <div>
      <div className='flex items-center gap-1 mb-2'>
        <Typography.Text strong className='text-sm'>
          {label}
        </Typography.Text>
        {required && <span className='text-red-500'>*</span>}
      </div>
      <input
        ref={inputRef}
        type='file'
        accept={accept || defaultAccept}
        className='hidden'
        onChange={handleFile}
        disabled={disabled}
      />
      {/* 未选文件时展示拖拽框;选好后收起拖拽框,只留预览 + 操作按钮(拖满即隐藏)。 */}
      {!value && (
        <div
          {...getRootProps()}
          onClick={() => !disabled && inputRef.current?.click()}
          className={[
            'flex flex-col items-center justify-center gap-1 rounded-lg border-2 border-dashed px-3 py-3 cursor-pointer transition-colors',
            isDragActive
              ? 'border-blue-400 bg-blue-50'
              : 'border-gray-200 hover:border-blue-300 hover:bg-gray-50',
            disabled ? 'opacity-50 cursor-not-allowed' : '',
          ].join(' ')}
        >
          <input {...getInputProps()} />
          <IconUpload
            size='large'
            className={isDragActive ? 'text-blue-400' : 'text-gray-400'}
          />
          <Typography.Text className='text-xs text-gray-500 text-center'>
            {isDragActive ? t('松开以添加文件') : idleText}
          </Typography.Text>
        </div>
      )}

      {/* 上传框之外单列一个拍摄入口:不放进拖拽区里,免得点它同时触发选文件。 */}
      {!value && canRecord && (
        <Button
          size='small'
          type='tertiary'
          theme='borderless'
          icon={<Video size={14} />}
          disabled={disabled}
          className='mt-1'
          onClick={() => setRecorderOpen(true)}
        >
          {t('现场拍摄')}
        </Button>
      )}

      {/* 已选文件:视频=缩略图预览(点击弹窗看原视频),音频=内联播放器。 */}
      {value &&
        (kind === 'video' ? (
          <div
            className='relative mt-1 inline-block cursor-pointer rounded-lg overflow-hidden border border-gray-200'
            onClick={() => setPreviewOpen(true)}
          >
            <video
              src={value}
              muted
              playsInline
              preload='metadata'
              className='block'
              style={{ width: 160, height: 100, objectFit: 'cover' }}
            />
            <div className='absolute inset-0 flex items-center justify-center bg-black/20'>
              <div className='w-9 h-9 rounded-full bg-black/50 flex items-center justify-center'>
                <Play size={18} className='text-white ml-0.5' />
              </div>
            </div>
          </div>
        ) : (
          <audio src={value} controls className='mt-2 w-full' />
        ))}

      {value && (
        <div className='flex items-center gap-1 mt-1'>
          <Button
            size='small'
            type='tertiary'
            theme='borderless'
            icon={<RefreshCw size={14} />}
            disabled={disabled}
            onClick={() => !disabled && inputRef.current?.click()}
          >
            {t('更换')}
          </Button>
          {canRecord && (
            <Button
              size='small'
              type='tertiary'
              theme='borderless'
              icon={<Video size={14} />}
              disabled={disabled}
              onClick={() => setRecorderOpen(true)}
            >
              {t('现场拍摄')}
            </Button>
          )}
          <Button
            size='small'
            type='danger'
            theme='borderless'
            icon={<X size={14} />}
            disabled={disabled}
            onClick={() => onChange('')}
          >
            {t('移除')}
          </Button>
        </div>
      )}

      {/* 原视频弹窗:带完整控制条,点击缩略图打开。 */}
      {kind === 'video' && (
        <Modal
          visible={previewOpen}
          onCancel={() => setPreviewOpen(false)}
          footer={null}
          centered
          title={label}
        >
          <video
            src={value}
            controls
            autoPlay
            className='w-full rounded-lg'
            style={{ maxHeight: '70vh' }}
          />
        </Modal>
      )}

      {/* 拍摄产物与上传走同一条路:一个 data-url 交回 onChange,上限也复用 maxMB。 */}
      {canRecord && (
        <VideoRecorderModal
          visible={recorderOpen}
          onClose={() => setRecorderOpen(false)}
          onConfirm={(dataUrl) => onChange(dataUrl)}
          maxMB={maxMB}
        />
      )}
    </div>
  );
};

export default MediaFileInput;
