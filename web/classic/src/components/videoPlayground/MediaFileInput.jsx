import React, { useRef } from 'react';
import { Button, Typography } from '@douyinfe/semi-ui';
import { Upload as UploadIcon, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { showError } from '../../helpers';

// 音频/视频单文件上传:读成 base64 data-url 交给上层(new-api 侧渠道会物化到 NFS,与
// 图生视频的帧图同机制)。带体积上限 + 试听/预览。kind: 'audio' | 'video'。
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

  const handleFile = (e) => {
    const file = e.target.files?.[0];
    e.target.value = ''; // 允许再次选同一文件
    if (!file) return;
    if (file.size > maxMB * 1024 * 1024) {
      showError(t('文件不能超过 {{size}} MB', { size: maxMB }));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => onChange(reader.result);
    reader.onerror = () => showError(t('读取文件失败'));
    reader.readAsDataURL(file);
  };

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
      <div className='flex items-center gap-2'>
        <Button
          size='small'
          icon={<UploadIcon size={14} />}
          disabled={disabled}
          onClick={() => inputRef.current?.click()}
        >
          {value ? t('重新选择') : t('选择文件')}
        </Button>
        {value && (
          <Button
            size='small'
            type='tertiary'
            theme='borderless'
            icon={<X size={14} />}
            disabled={disabled}
            onClick={() => onChange('')}
          >
            {t('移除')}
          </Button>
        )}
      </div>
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
