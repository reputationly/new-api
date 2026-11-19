import { useEffect, useState } from 'react';
import { Banner, Modal, Spin, Tag, Typography } from '@douyinfe/semi-ui';

import { API } from '../../../../helpers';

const { Text } = Typography;

// 命名体检：列出「仅大小写不同」的模型名。
//
// 这类名字在系统里是彼此独立的模型 —— 各自有 ability、各自占模型广场与体验区下拉里
// 的一项、各自按**精确名**匹配倍率。后果有两层：用户在下拉里看到两个一模一样的模型；
// 更实际的是只配了一边的大小写时，另一边的请求会静默按默认倍率（ratio=1）计费。
//
// 只读展示，不自动合并：改名会牵动计费键与令牌白名单，必须由人确认。
const CaseVariantModal = ({ visible, onClose, t }) => {
  const [loading, setLoading] = useState(false);
  const [groups, setGroups] = useState([]);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      setError('');
      try {
        const res = await API.get('/api/models/case_variants', {
          skipErrorHandler: true,
        });
        if (cancelled) return;
        if (res?.data?.success) {
          setGroups(res.data.data?.groups || []);
        } else {
          setError(res?.data?.message || t('获取命名体检结果失败'));
        }
      } catch (e) {
        if (!cancelled) {
          setError(e?.response?.data?.message || t('获取命名体检结果失败'));
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    return () => {
      cancelled = true;
    };
  }, [visible, t]);

  return (
    <Modal
      title={
        <div className='flex items-center gap-2'>
          <Typography.Text
            strong
            className='!text-[var(--semi-color-text-0)] !text-base'
          >
            {t('命名体检')}
          </Typography.Text>
          {!loading && !error && groups.length > 0 && (
            <Typography.Text type='tertiary' size='small'>
              {t('共')} {groups.length} {t('组仅大小写不同的模型名')}
            </Typography.Text>
          )}
        </div>
      }
      visible={visible}
      onCancel={onClose}
      footer={null}
      size='medium'
      className='!rounded-lg'
    >
      {loading && (
        <div className='py-8 flex justify-center'>
          <Spin />
        </div>
      )}

      {error && <Banner type='danger' closeIcon={null} description={error} />}

      {!loading && !error && groups.length === 0 && (
        <Text type='tertiary'>{t('没有发现仅大小写不同的模型名。')}</Text>
      )}

      {!loading && !error && groups.length > 0 && (
        <>
          <Banner
            type='warning'
            closeIcon={null}
            description={t(
              '以下名字仅大小写不同，但在系统里是彼此独立的模型：各自计价、各占一个模型下拉项。只配了一边的大小写时，另一边会按默认倍率计费。建议收敛成一个名字。',
            )}
          />
          <div className='mt-3 flex flex-col gap-3'>
            {groups.map((group) => (
              <div
                key={group.name_lower}
                className='rounded-lg border border-[var(--semi-color-border)] p-3'
              >
                <Text strong>{group.name_lower}</Text>
                <div className='mt-2 flex flex-col gap-2'>
                  {group.variants.map((v) => (
                    <div
                      key={v.model}
                      className='flex items-center gap-2 flex-wrap'
                    >
                      <Text size='small'>{v.model}</Text>
                      <Tag size='small' color={v.enabled ? 'green' : 'grey'}>
                        {v.enabled ? t('有启用渠道') : t('全部禁用')}
                      </Tag>
                      <Tag size='small'>#{v.channel_ids?.join(', #')}</Tag>
                      <Tag
                        size='small'
                        color={v.ratio_configured ? 'blue' : 'orange'}
                      >
                        {v.ratio_configured
                          ? t('已配倍率')
                          : t('未配倍率（按默认值计费）')}
                      </Tag>
                      {v.price_configured && (
                        <Tag size='small' color='blue'>
                          {t('已配固定价')}
                        </Tag>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </Modal>
  );
};

export default CaseVariantModal;
