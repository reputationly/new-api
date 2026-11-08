import React, { useContext } from 'react';
import { Empty, Spin } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';

const Assistant = () => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const assistantLink = statusState?.status?.assistant_link || '';

  if (statusState?.status === undefined) {
    return (
      <div className='mt-[64px] flex items-center justify-center h-screen'>
        <Spin size='large' />
      </div>
    );
  }

  if (!assistantLink) {
    return (
      <div className='mt-[64px] flex items-center justify-center h-screen p-8'>
        <Empty description={t('管理员暂未配置蒜狸小助手页面地址')} />
      </div>
    );
  }

  return (
    <div className='mt-[64px] px-2'>
      <div className='flex justify-end px-2 py-2 text-sm'>
        <a
          href={assistantLink}
          target='_blank'
          rel='noopener noreferrer'
          className='!text-semi-color-primary'
        >
          {t('页面无法显示？在新标签页打开')}
        </a>
      </div>
      <iframe
        src={assistantLink}
        title={t('蒜狸小助手')}
        style={{
          width: '100%',
          height: 'calc(100vh - 110px)',
          border: 'none',
        }}
      />
    </div>
  );
};

export default Assistant;
