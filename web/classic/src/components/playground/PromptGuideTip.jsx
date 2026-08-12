import React, { useContext, useMemo, useState } from 'react';
import { Button, Popover, Toast, Typography } from '@douyinfe/semi-ui';
import { Check, Copy, HelpCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { copy } from '../../helpers';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import {
  getTabPromptGuide,
  parsePlaygroundTabConfig,
} from '../../constants/playgroundAdmin.constants';
import { defaultPromptGuide } from '../../constants/promptGuide.constants';

// 左侧「模型配置」标题行里的「怎么写提示词」小问号，悬停向右展开该玩法的写作建议。
//
// 挂在配置面板而不是提示词框上方：提示词框那一排本就挤着示例按钮与 AI 优化按钮，再加
// 一行把输入区越推越低；而建议本身是「这个玩法怎么用」的说明，与分组/模型的说明同属
// 配置面板。位置一挪，浮层也就能往右铺开，不必再挤在输入框上方那点高度里。
//
// 入参与 PromptOptimizeButton 完全一致（category + tabKey），所以「视频配音」这种
// 入口在语音页、内容走视频组件的玩法不用特判——两处传的是同一对键。
//
// 取值与「AI 优化提示词」的系统提示词同构：运营改写过就用他的，留空则退回内置默认
// （promptGuide.constants.js）。两处都空 = 整体不渲染，而不是给一个点开是空的问号。
//
// 光一个问号图标没人会去悬停，所以旁边带一句灰字点题；整块都是悬停区。
//
// engine 只有视频体验区传（所选模型的引擎族）：MiniMax H3 的提示词区是三段式、还有几段
// 系统自动生成，与通用的单框写法对不上，故建议文案也跟着分岔。不传即通用版。
const PromptGuideTip = ({ category, tabKey, engine }) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [copied, setCopied] = useState(false);
  const isMobile = useIsMobile();
  const raw = statusState?.status?.PlaygroundTabConfig;
  const guide = useMemo(() => {
    const custom = getTabPromptGuide(
      parsePlaygroundTabConfig(raw),
      category,
      tabKey,
    ).trim();
    return custom || defaultPromptGuide(tabKey, engine).trim();
  }, [raw, category, tabKey, engine]);

  const handleCopy = async () => {
    if (await copy(guide)) {
      setCopied(true);
      Toast.success(t('已复制到剪贴板'));
      setTimeout(() => setCopied(false), 2000);
    } else {
      Toast.error(t('复制失败'));
    }
  };

  if (!guide) return null;
  return (
    // Tooltip 换成 Popover：里面有个复制按钮，Tooltip 的浮层不是为可点内容准备的。
    <Popover
      // 手机上配置面板是整屏宽、堆在页面顶部，右边没有地方开横板（Semi 见左右都放不下
      // 会把浮层钉到 body 左缘，位置不算错，但 16:9 只剩一百多像素高，几十行建议要滚
      // 十几屏）。故窄屏改为往下开、放弃比例、按视口给高度。
      position={isMobile ? 'bottom' : 'rightTop'}
      showArrow
      content={
        // 桌面固定成一块 16:9 的横板，而不是让浮层宽度跟着文字走 —— 跟着走会被最长的
        // 那条示例撑成一个又瘦又高、几十行的条子。写死宽度后一条建议基本一行放得下，
        // 读起来是一段一段而不是一行几个字。内置的那几份有二十来行，超出部分在框内
        // 滚动（鼠标移到浮层上不会收起，能滚着读完）。
        <div
          className='flex flex-col'
          style={
            isMobile
              ? { width: 'min(92vw, 420px)', maxHeight: '60vh' }
              : {
                  // 宽度让出左侧面板那一栏（浮层是往右开的）；高度由 16:9 推出，不写死，
                  // 免得宽度被收窄后比例走样。
                  width: 'clamp(320px, calc(100vw - 360px), 720px)',
                  aspectRatio: '16 / 9',
                  maxHeight: '70vh',
                }
          }
        >
          <div className='flex items-center justify-between gap-2 px-4 py-2 border-b border-gray-100'>
            <Typography.Text strong className='text-sm'>
              {t('怎么写提示词')}
            </Typography.Text>
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={copied ? <Check size={14} /> : <Copy size={14} />}
              onClick={handleCopy}
            >
              {copied ? t('已复制') : t('复制')}
            </Button>
          </div>
          <div
            className='flex-1 min-h-0 overflow-y-auto px-4 py-3 text-sm'
            style={{ whiteSpace: 'pre-wrap', lineHeight: 1.7 }}
          >
            {guide}
          </div>
        </div>
      }
    >
      <span className='flex items-center gap-1 cursor-help'>
        <Typography.Text type='tertiary' className='text-xs'>
          {t('怎么写提示词')}
        </Typography.Text>
        <HelpCircle size={14} className='text-gray-400' />
      </span>
    </Popover>
  );
};

export default PromptGuideTip;
