import React, { useContext, useEffect, useState } from 'react';
import { Empty, Spin } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';

/** 超过这个时间还没加载完，才把「在新标签页打开」的兜底提示露出来。 */
const LOAD_TIMEOUT_MS = 6000;

const Assistant = () => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const assistantLink = statusState?.status?.assistant_link || '';
  const [loaded, setLoaded] = useState(false);
  const [timedOut, setTimedOut] = useState(false);

  // 兜底提示只在 iframe 真的没加载出来时出现；否则它常驻在导航栏下方，看起来像页面坏了。
  useEffect(() => {
    if (!assistantLink) return undefined;
    const timer = window.setTimeout(() => setTimedOut(true), LOAD_TIMEOUT_MS);
    return () => window.clearTimeout(timer);
  }, [assistantLink]);

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

  const showFallback = timedOut && !loaded;

  return (
    <div className='mt-[64px] px-2'>
      {showFallback && (
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
      )}
      <iframe
        src={assistantLink}
        title={t('蒜狸小助手')}
        onLoad={() => setLoaded(true)}
        style={{
          width: '100%',
          height: showFallback
            ? 'calc(100vh - 110px)'
            : 'calc(100vh - 88px)',
          border: 'none',
        }}
      />
    </div>
  );
};

export default Assistant;
