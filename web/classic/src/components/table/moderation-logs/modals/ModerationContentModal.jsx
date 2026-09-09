import React from 'react';
import { Banner, Modal, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

const { Paragraph } = Typography;

/**
 * 查看被拦原文。
 *
 * 内容只在打开弹窗时按需拉取、关闭即丢弃：后端每次返回原文都会写一条管理操作审计，
 * 一旦缓存下来重开不请求，「谁看了哪条」就漏记了，而留痕是这个入口唯一的约束。
 */
const ModerationContentModal = ({ visible, loading, content, onClose, t }) => {
  const { t: defaultT } = useTranslation();
  const translate = t || defaultT;

  return (
    <Modal
      title={translate('查看被拦原文')}
      visible={visible}
      onCancel={onClose}
      footer={null}
      width={720}
    >
      <Banner
        type='info'
        closeIcon={null}
        description={translate('本次查看已记录到管理操作日志。')}
        style={{ marginBottom: 16 }}
      />
      <Spin spinning={loading}>
        <div
          style={{
            maxHeight: 480,
            overflow: 'auto',
            padding: 12,
            borderRadius: 6,
            background: 'var(--semi-color-fill-0)',
          }}
        >
          <Paragraph style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            {content || (loading ? '' : translate('（无内容）'))}
          </Paragraph>
        </div>
      </Spin>
    </Modal>
  );
};

export default ModerationContentModal;
