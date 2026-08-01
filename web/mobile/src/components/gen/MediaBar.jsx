import React, { useRef, useState } from 'react';
import { Button, Image, ImageViewer } from 'antd-mobile';
import {
  AddOutline,
  AudioOutline,
  CameraOutline,
  VideoOutline,
} from 'antd-mobile-icons';

import VoiceRecorder from './VoiceRecorder';
import { showError } from '../../shims/classic-utils';
import { fileToDataUrl, readMediaDuration } from '../../utils/file';

// 手机上传源视频要整段 base64 塞进请求体，弱网下大文件几乎必挂。后台给桌面端配的
// maxInputMB 可能到 30~50MB，移动端再收一道闸，超了直接拦下并提示裁剪。
export const MOBILE_MAX_VIDEO_MB = 20;

// 现场录制的时长上限：录像走系统相机（HTML 无法限制，只能录完校验后拦下），
// 录音走 useVoiceRecorder（到点自动停）。
export const MOBILE_RECORD_VIDEO_MAX_SEC = 10;
export const MOBILE_RECORD_AUDIO_MAX_SEC = 20;
export const MOBILE_RECORD_AUDIO_MIN_SEC = 1;

const ACCEPT = { image: 'image/*', audio: 'audio/*', video: 'video/*' };
const KIND_NAME = { image: '图片', audio: '音频', video: '视频' };
// capture 让移动端浏览器直接唤起相机/摄像；桌面浏览器会忽略它，退化成普通选文件。
const CAPTURE_LABEL = { image: '拍照', video: '录像', audio: '录音' };
const CAPTURE_ICON = {
  image: CameraOutline,
  video: VideoOutline,
  audio: AudioOutline,
};
const PICK_LABEL = { image: '从相册选', video: '从相册选', audio: '选择文件' };

// 校验并转换成 data-url。fromCapture=true 时额外卡录像时长。
const acceptFile = async (file, { kind, label, maxMB, fromCapture }) => {
  if (maxMB > 0 && file.size > maxMB * 1024 * 1024) {
    showError(`${label}不能超过 ${maxMB}MB，请压缩或裁剪后再上传`);
    return null;
  }
  if (kind === 'video' && fromCapture) {
    const duration = await readMediaDuration(file, 'video');
    if (Number.isFinite(duration) && duration > MOBILE_RECORD_VIDEO_MAX_SEC) {
      showError(
        `录像不能超过 ${MOBILE_RECORD_VIDEO_MAX_SEC} 秒（本次 ${Math.round(duration)} 秒），请重录`,
      );
      return null;
    }
  }
  try {
    return await fileToDataUrl(file);
  } catch (e) {
    showError(`读取${KIND_NAME[kind]}失败`);
    return null;
  }
};

// 「现场采集 + 选文件」两个入口。图片=拍照/相册，视频=录像/相册，音频=录音/选文件。
// 音频不用 capture：安卓各家自带录音机行为不一，走我们自己的录音弹层（能控上限、能试听）。
const PickerActions = ({
  kind,
  label,
  maxMB,
  disabled,
  compact,
  onPicked,
  onRecordAudio,
}) => {
  const pickRef = useRef(null);
  const captureRef = useRef(null);

  const handleChange = (fromCapture) => async (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    const dataUrl = await acceptFile(file, {
      kind,
      label,
      maxMB,
      fromCapture,
    });
    if (dataUrl) onPicked(dataUrl, file.name);
  };

  const size = compact ? 'mini' : 'small';
  const CaptureIcon = CAPTURE_ICON[kind];

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <Button
        size={size}
        fill='outline'
        disabled={disabled}
        onClick={() =>
          kind === 'audio' ? onRecordAudio() : captureRef.current?.click()
        }
      >
        <CaptureIcon /> {CAPTURE_LABEL[kind]}
      </Button>
      <Button
        size={size}
        fill='outline'
        disabled={disabled}
        onClick={() => pickRef.current?.click()}
      >
        {PICK_LABEL[kind]}
      </Button>
      <input
        ref={pickRef}
        type='file'
        accept={ACCEPT[kind]}
        hidden
        onChange={handleChange(false)}
      />
      {kind !== 'audio' && (
        <input
          ref={captureRef}
          type='file'
          accept={ACCEPT[kind]}
          capture='environment'
          hidden
          onChange={handleChange(true)}
        />
      )}
    </div>
  );
};

// 单个媒体输入格：采集/选文件 → 校验 → 转 data-url 回填。
// 图片给缩略图（点开看原图），音/视频用原生播放器（手机上比自绘控件可靠）。
const MediaSlot = ({
  label,
  kind = 'image',
  value,
  name,
  onChange,
  maxMB = 0,
  disabled = false,
  required = false,
}) => {
  const [viewerOpen, setViewerOpen] = useState(false);
  const [recorderOpen, setRecorderOpen] = useState(false);

  const actions = (
    <PickerActions
      kind={kind}
      label={label}
      maxMB={maxMB}
      disabled={disabled}
      compact={!!value}
      onPicked={onChange}
      onRecordAudio={() => setRecorderOpen(true)}
    />
  );

  return (
    <div className='m-media-slot'>
      <div className='m-media-slot-label'>
        {label}
        {required && <span style={{ color: 'var(--adm-color-danger)' }}>*</span>}
      </div>
      {value ? (
        <div>
          {kind === 'image' && (
            <div style={{ position: 'relative', display: 'inline-block' }}>
              <Image
                src={value}
                width={72}
                height={72}
                fit='cover'
                style={{ borderRadius: 8 }}
                onClick={() => setViewerOpen(true)}
              />
              <ImageViewer
                image={value}
                visible={viewerOpen}
                onClose={() => setViewerOpen(false)}
              />
            </div>
          )}
          {kind === 'audio' && (
            <audio
              src={value}
              controls
              preload='none'
              style={{ width: '100%' }}
            />
          )}
          {kind === 'video' && (
            <video
              src={value}
              controls
              playsInline
              preload='metadata'
              style={{ width: '100%', maxHeight: 140, borderRadius: 8 }}
            />
          )}
          <div className='m-media-slot-actions'>
            {actions}
            <Button
              size='mini'
              fill='outline'
              disabled={disabled}
              onClick={() => onChange('', '')}
            >
              移除
            </Button>
            {name && <span className='m-media-slot-name'>{name}</span>}
          </div>
        </div>
      ) : (
        actions
      )}
      {kind === 'audio' && (
        <VoiceRecorder
          visible={recorderOpen}
          onClose={() => setRecorderOpen(false)}
          onConfirm={(dataUrl) => onChange(dataUrl, '录制音频.wav')}
          title={`录制${label}`}
          script=''
          minSeconds={MOBILE_RECORD_AUDIO_MIN_SEC}
          maxSeconds={MOBILE_RECORD_AUDIO_MAX_SEC}
        />
      )}
    </div>
  );
};

// 多图输入格（参考图 / 底图）：相册一次可多选，拍照一次一张，累计不超过 max 张。
const MediaListSlot = ({
  label,
  values = [],
  onChange,
  max = 3,
  maxMB = 0,
  disabled = false,
  required = false,
}) => {
  const pickRef = useRef(null);
  const captureRef = useRef(null);
  const [viewer, setViewer] = useState('');
  const full = values.length >= max;

  const handlePick = async (e) => {
    const files = Array.from(e.target.files || []);
    e.target.value = '';
    if (!files.length) return;
    if (maxMB > 0 && files.some((f) => f.size > maxMB * 1024 * 1024)) {
      showError(`${label}单张不能超过 ${maxMB}MB`);
      return;
    }
    try {
      const urls = await Promise.all(files.map(fileToDataUrl));
      onChange([...values, ...urls].slice(0, max));
    } catch (err) {
      showError('读取图片失败');
    }
  };

  const removeAt = (idx) => {
    const next = [...values];
    next.splice(idx, 1);
    onChange(next);
  };

  return (
    <div className='m-media-slot'>
      <div className='m-media-slot-label'>
        {label}
        {required && <span style={{ color: 'var(--adm-color-danger)' }}>*</span>}
        <span className='m-media-slot-hint'>
          {values.length}/{max}
        </span>
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {values.map((url, idx) => (
          <div key={idx} style={{ position: 'relative' }}>
            <Image
              src={url}
              width={72}
              height={72}
              fit='cover'
              style={{ borderRadius: 8 }}
              onClick={() => setViewer(url)}
            />
            <Button
              size='mini'
              disabled={disabled}
              style={{ position: 'absolute', top: -8, right: -8 }}
              onClick={() => removeAt(idx)}
            >
              ×
            </Button>
          </div>
        ))}
        {!full && (
          <>
            <Button
              size='small'
              fill='outline'
              disabled={disabled}
              onClick={() => captureRef.current?.click()}
            >
              <CameraOutline /> 拍照
            </Button>
            <Button
              size='small'
              fill='outline'
              disabled={disabled}
              onClick={() => pickRef.current?.click()}
            >
              <AddOutline /> 从相册选
            </Button>
          </>
        )}
      </div>
      <ImageViewer
        image={viewer}
        visible={!!viewer}
        onClose={() => setViewer('')}
      />
      <input
        ref={pickRef}
        type='file'
        accept='image/*'
        multiple
        hidden
        onChange={handlePick}
      />
      <input
        ref={captureRef}
        type='file'
        accept='image/*'
        capture='environment'
        hidden
        onChange={handlePick}
      />
    </div>
  );
};

// 各玩法所需媒体输入的容器：贴在参数条下方，纵向排布（一屏内可见，不占底部输入条）。
// slots 形如 [{ type:'single'|'list'|'custom', key, ... }]，falsy 项直接跳过，
// 这样调用方可以写 `isS2V && {...}` 而不必先过滤。整条为空时不渲染。
const MediaBar = ({ slots = [], notice = '', disabled = false }) => {
  const visible = slots.filter(Boolean);
  if (!visible.length && !notice) return null;

  return (
    <div className='m-media-bar'>
      {notice && <div className='m-media-notice'>{notice}</div>}
      {visible.map(({ type, key, render, ...rest }) => {
        if (type === 'custom') {
          return (
            <div key={key} className='m-media-slot'>
              {rest.label && (
                <div className='m-media-slot-label'>{rest.label}</div>
              )}
              {render}
            </div>
          );
        }
        const Slot = type === 'list' ? MediaListSlot : MediaSlot;
        return <Slot key={key} disabled={disabled} {...rest} />;
      })}
    </div>
  );
};

export default MediaBar;
