import React, { useContext, useMemo } from 'react';
import { Tooltip, Typography } from '@douyinfe/semi-ui';
import { HelpCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import {
  getTabPromptGuide,
  parsePlaygroundTabConfig,
} from '../../constants/playgroundAdmin.constants';
import { defaultPromptGuide } from '../../constants/promptGuide.constants';

// 提示词框上方的「怎么写提示词」小问号，悬停展开该玩法的提示词写作建议。
//
// 入参与 PromptOptimizeButton 完全一致（category + tabKey），所以「视频配音」这种
// 入口在语音页、内容走视频组件的玩法不用特判——两处传的是同一对键。
//
// 取值与「AI 优化提示词」的系统提示词同构：运营改写过就用他的，留空则退回内置默认
// （promptGuide.constants.js）。两处都空 = 整体不渲染，而不是给一个点开是空的问号。
//
// 光一个问号图标没人会去悬停，所以旁边带一句灰字点题；整块都是悬停区。
const PromptGuideTip = ({ category, tabKey }) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const raw = statusState?.status?.PlaygroundTabConfig;
  const guide = useMemo(() => {
    const custom = getTabPromptGuide(
      parsePlaygroundTabConfig(raw),
      category,
      tabKey,
    ).trim();
    return custom || defaultPromptGuide(tabKey).trim();
  }, [raw, category, tabKey]);
  if (!guide) return null;
  return (
    <div className='flex items-center gap-1 mb-2'>
      <Tooltip
        position='top'
        content={
          // 分条写的建议原样保留换行；内置的那几份有二十来行，限高防止顶出视口
          // （鼠标移到浮层上不会收起，能滚着读完）。
          <div
            style={{
              maxWidth: 360,
              maxHeight: '60vh',
              overflowY: 'auto',
              whiteSpace: 'pre-wrap',
            }}
          >
            {guide}
          </div>
        }
      >
        <span className='flex items-center gap-1 cursor-help'>
          <Typography.Text type='tertiary' className='text-xs'>
            {t('怎么写提示词')}
          </Typography.Text>
          <HelpCircle size={14} className='text-gray-400' />
        </span>
      </Tooltip>
    </div>
  );
};

export default PromptGuideTip;
