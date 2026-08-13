import React from 'react';
import { Button, Typography } from '@douyinfe/semi-ui';
import { Sparkles } from 'lucide-react';

// 体验区「让 AI 先帮我写一版」类按钮的统一外观与交互。目前两个调用方:
//   - PromptOptimizeButton(图像/视频/音效的「AI 优化提示词」)
//   - MusicChatArea(文生音乐的「AI 帮我写词」)
// 两者产出的东西不同(一个回一段正文、一个回一份 JSON 方案),但对用户而言是同一件事:
// 点一下、等几秒、结果回填、可再改;用的也是同一个运营配的语言模型(「体验区管理 →
// 通用设置」里的优化模型)。此前各写各的,于是图标、加载态文案、空输入的处理、在途提示
// 全都不一样 —— 抽出来就是为了不再各自漂移。
//
// 交互约定(两处必须一致):
//   - 空输入**不置灰**:置灰说不清为什么点不动,不如点了给一句「先写个大概方向」;
//   - 在途时按钮转圈 + 换文案,右侧说明转成 warning 色,提醒别刷新页面;
//   - 在途时调用方要一并灰掉发送按钮 —— 此刻发出去的还是没经 AI 处理的原文。
const AiAssistButton = ({
  label,
  busyLabel,
  hint,
  busyHint,
  busy = false,
  disabled = false,
  onClick,
}) => (
  <div className='flex items-center gap-2 mb-2'>
    <Button
      theme='borderless'
      type='primary'
      size='small'
      icon={<Sparkles size={14} />}
      loading={busy}
      disabled={disabled || busy}
      onClick={onClick}
    >
      {busy ? busyLabel : label}
    </Button>
    <Typography.Text type={busy ? 'warning' : 'tertiary'} className='text-xs'>
      {busy ? busyHint : hint}
    </Typography.Text>
  </div>
);

export default AiAssistButton;
