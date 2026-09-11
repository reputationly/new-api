import React from 'react';
import { Banner, Modal, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

/**
 * 查看被拦的图片/视频。
 *
 * 与查看原文同一套约束：媒体取证材料就是原文，只是换了个模态。链接按需签发、
 * 关闭即丢弃——后端每次签发都会写一条管理操作审计，缓存下来重开不请求就等于
 * 「看了但没留痕」，而留痕是这个入口唯一的约束。
 *
 * 展示的是**原始上传物**，不是判定时抽的那三帧：帧只在判定过程中存在，没有留存。
 * 所以视频要靠人自己拖进度条找违规画面，判定说的「任一帧违规」未必落在开头。
 */
const ModerationMediaModal = ({
  visible,
  loading,
  url,
  isVideo,
  onClose,
  t,
}) => {
  const { t: defaultT } = useTranslation();
  const translate = t || defaultT;

  return (
    <Modal
      title={translate('查看被拦媒体')}
      visible={visible}
      onCancel={onClose}
      footer={null}
      width={860}
    >
      <Banner
        type='info'
        closeIcon={null}
        description={translate('本次查看已记录到管理操作日志。')}
        style={{ marginBottom: 16 }}
      />
      {isVideo && (
        <Banner
          type='warning'
          closeIcon={null}
          description={translate(
            '这是用户上传的原始视频。判定只抽取了首/中/尾三帧，命中的画面未必在开头，需要自行拖动查看。',
          )}
          style={{ marginBottom: 16 }}
        />
      )}
      <Spin spinning={loading}>
        <div
          style={{
            minHeight: 200,
            maxHeight: 560,
            overflow: 'auto',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 12,
            borderRadius: 6,
            background: 'var(--semi-color-fill-0)',
          }}
        >
          {!url && !loading && (
            <Text type='tertiary'>{translate('（无内容）')}</Text>
          )}
          {url && isVideo && (
            <video
              src={url}
              controls
              style={{ maxWidth: '100%', maxHeight: 520 }}
            />
          )}
          {url && !isVideo && (
            <img
              src={url}
              alt={translate('被拦媒体')}
              style={{ maxWidth: '100%', maxHeight: 520, objectFit: 'contain' }}
            />
          )}
        </div>
      </Spin>
    </Modal>
  );
};

export default ModerationMediaModal;
