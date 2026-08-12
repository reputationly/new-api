import React, { useState } from 'react';
import { Button, TextArea, Typography } from '@douyinfe/semi-ui';
import { ChevronDown, ChevronRight, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  H3_SECTION_LABELS,
  isH3DerivedSection,
  isH3MainSection,
} from '../../constants/h3Prompt.constants';

// MiniMax H3 的优化结果展示：按字段名切成几段可折叠的块。
//
// 为什么不平铺成一个大文本框：H3 的提示词是「画面描述 + 音景 + 背景音乐」（参考生视频
// 还多三段参考声明）拼起来的几百词英文，塞进一个框里既没法看也没法改。折叠后画面描述
// 默认展开、声音两段收起，用户想调哪段点开哪段。
//
// **切不出结构时本组件根本不会被渲染**——调用方拿 parseH3Prompt 的 null 降级成单框显示
// 原文。模型偶尔不按格式返回是常态，那种时候白屏或吞掉结果都是不能接受的。
//
// 两类段落分开对待（isH3DerivedSection）：
//   派生段（对齐指令 / 素材定义 / 概述 / 保留分析）—— 只读。它们是从素材与参数推出来
//   的事实，不是创作：对齐指令里的秒数、<Picture 1> 的首尾角色、哪个素材 fully_preserved，
//   用户手改一律是改错，而 H3 对 prompt 不做解析，改错了不报错、只默默出差档。
//   仍然完整回拼提交，只是不给编辑入口。
//   其余段 —— 可编辑，并把用户当初填的那段中文原文摆在上面做对照，
//   省得他对着几百词英文猜这段是不是自己想要的。
//
// 所见即所发：这里显示的几段就是提交时回拼出去的内容。
const OptimizedPromptSections = ({
  sections,
  sourceByKey,
  onChange,
  onDiscard,
  disabled = false,
}) => {
  const { t } = useTranslation();
  // 只记用户手动翻过的块；没翻过的按「正文展开、其余收起」的默认走。这样重新优化一次
  // 之后展开状态还在（键没变），不会每次都被打回默认。
  const [toggled, setToggled] = useState({});
  if (!sections?.length) return null;

  const isOpen = (sec, i) =>
    toggled[`${i}:${sec.key}`] ?? isH3MainSection(sec.key);

  return (
    <div className='mt-2 rounded-xl border border-gray-200 overflow-hidden'>
      <div className='flex items-center justify-between px-3 py-1.5 bg-gray-50'>
        <Typography.Text type='tertiary' className='text-xs'>
          {t('优化结果：提交的是下面这几段，上方输入框只用于重新优化')}
        </Typography.Text>
        <Button
          theme='borderless'
          type='tertiary'
          size='small'
          icon={<X size={12} />}
          disabled={disabled}
          onClick={onDiscard}
          className='!text-gray-500'
        >
          {t('弃用')}
        </Button>
      </div>
      {sections.map((sec, i) => {
        const open = isOpen(sec, i);
        const derived = isH3DerivedSection(sec.key);
        const source = (sourceByKey?.[sec.key] || '').trim();
        return (
          <div key={`${i}:${sec.key}`} className='border-t border-gray-200'>
            <button
              type='button'
              onClick={() =>
                setToggled((prev) => ({ ...prev, [`${i}:${sec.key}`]: !open }))
              }
              className='w-full flex items-center gap-1.5 px-3 py-2 text-left hover:bg-gray-50 transition-colors'
            >
              {open ? (
                <ChevronDown size={14} className='shrink-0 text-gray-500' />
              ) : (
                <ChevronRight size={14} className='shrink-0 text-gray-500' />
              )}
              <span className='text-xs font-medium text-gray-700 shrink-0'>
                {t(H3_SECTION_LABELS[sec.key] || sec.key)}
              </span>
              {/* 派生段标一下「自动生成」：不然只读会被当成界面坏了 */}
              {derived && (
                <span className='text-[10px] text-gray-400 shrink-0 px-1 py-0.5 rounded bg-gray-100'>
                  {t('自动生成')}
                </span>
              )}
              {/* 收起时给一行摘要：不点开也知道这段里是什么 */}
              {!open && (
                <span className='text-xs text-gray-400 truncate min-w-0'>
                  {sec.value}
                </span>
              )}
            </button>
            {open && (
              <div className='px-3 pb-2'>
                {/* 中英对照：用户填的那段中文摆在英文上面，不占编辑位、也不参与提交 */}
                {!derived && source && (
                  <div className='mb-1.5 px-2 py-1.5 rounded-lg bg-gray-50 text-xs text-gray-500 whitespace-pre-wrap'>
                    {source}
                  </div>
                )}
                {derived ? (
                  <div className='px-2 py-1.5 rounded-lg bg-gray-50 text-xs text-gray-600 whitespace-pre-wrap break-words'>
                    {sec.value}
                  </div>
                ) : (
                  <TextArea
                    value={sec.value}
                    onChange={(v) =>
                      onChange(
                        sections.map((s, j) =>
                          j === i ? { ...s, value: v } : s,
                        ),
                      )
                    }
                    disabled={disabled}
                    autosize={{ minRows: 2, maxRows: 8 }}
                    className='!rounded-lg'
                  />
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

export default OptimizedPromptSections;
