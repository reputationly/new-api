import React, { useRef } from 'react';
import { Button, Typography } from '@douyinfe/semi-ui';
import { IconUpload } from '@douyinfe/semi-icons';
import { X } from 'lucide-react';
import { useDropzone } from 'react-dropzone';
import { useTranslation } from 'react-i18next';
import { showError } from '../../helpers';

// 音频/视频单文件上传:读成 base64 data-url 交给上层(new-api 侧渠道会物化到 NFS,与
// 图生视频的帧图同机制)。支持点击选择与拖拽上传,带体积上限 + 试听/预览。
// kind: 'audio' | 'video'。
const MediaFileInput = ({
  label,
  required = false,
  kind = 'audio',
  value = '', // base64 data-url 或 ''
  accept,
  maxMB = 50,
  disabled = false,
  onChange,
}) => {
  const { t } = useTranslation();
  const inputRef = useRef(null);

  const defaultAccept =
    kind === 'video' ? 'video/*,.mp4,.mov,.webm' : 'audio/*,.wav,.mp3,.m4a';

  const readFile = (file) => {
    if (!file) return;
    if (maxMB > 0 && file.size > maxMB * 1024 * 1024) {
      showError(t('文件不能超过 {{size}} MB', { size: maxMB }));
      return;
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
          {isDragActive
            ? t('松开以添加文件')
            : value
              ? t('拖拽或点击以重新选择')
              : idleText}
        </Typography.Text>
      </div>
      {value && (
        <Button
          size='small'
          type='tertiary'
          theme='borderless'
          icon={<X size={14} />}
          disabled={disabled}
          onClick={() => onChange('')}
          className='mt-1'
        >
          {t('移除')}
        </Button>
      )}
      {value &&
        (kind === 'video' ? (
          <video
            src={value}
            controls
            className='mt-2 w-full rounded-lg'
            style={{ maxHeight: 160 }}
          />
        ) : (
          <audio src={value} controls className='mt-2 w-full' />
        ))}
    </div>
  );
};

export default MediaFileInput;
