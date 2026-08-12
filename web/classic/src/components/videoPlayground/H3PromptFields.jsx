import React, { useState } from 'react';
import { TextArea, Typography } from '@douyinfe/semi-ui';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { H3_INPUT_FIELDS } from '../../constants/h3Prompt.constants';

// MiniMax H3 的输入侧补充两段:音景 / 背景音乐,默认折叠,挂在提示词框下方。
//
// 为什么把结构摆到输入侧:改动前是「写一句中文 → 点优化 → 突然弹出一堆英文分段」,
// 用户事前根本不知道 H3 除了画面还要音景与配乐,也就无从写起。三段摆出来等于把格式
// 教给用户,他想管哪段点开哪段。
//
// 为什么这里只有两段、画面描述不在这里:画面描述沿用原来那个提示词输入框(带发送
// 按钮与回车发送),换掉它会把整块输入区重排一遍,得不偿失。三段的关系靠输入框上方
// 那个「画面描述」小标题点明。
//
// 留空是正常用法,不是没填完:空段在提交时整段省略(见 buildLocalH3Prompt),模型自己
// 配。**刻意不补 `N/A`** —— 按 guide 那是「明确要求全程静音 / 无配乐」的意思,与
// 「随便你」不是一回事。占位文案里把这点写明了。
const H3PromptFields = ({ values, onChange, disabled = false }) => {
  const { t } = useTranslation();
  const [openKeys, setOpenKeys] = useState({});
  const fields = H3_INPUT_FIELDS.filter((f) => !f.defaultOpen);

  return (
    <div className='mt-2 rounded-xl border border-gray-200 overflow-hidden'>
      {fields.map((f, i) => {
        const open = openKeys[f.key] ?? f.defaultOpen;
        const value = values?.[f.key] || '';
        return (
          <div
            key={f.key}
            className={i > 0 ? 'border-t border-gray-200' : undefined}
          >
            <button
              type='button'
              onClick={() =>
                setOpenKeys((prev) => ({ ...prev, [f.key]: !open }))
              }
              className='w-full flex items-center gap-1.5 px-3 py-2 text-left hover:bg-gray-50 transition-colors'
            >
              {open ? (
                <ChevronDown size={14} className='shrink-0 text-gray-500' />
              ) : (
                <ChevronRight size={14} className='shrink-0 text-gray-500' />
              )}
              <span className='text-xs font-medium text-gray-700 shrink-0'>
                {t(f.label)}
              </span>
              {/* 收起时:填了就给一行摘要,没填就点明留空的后果——不然用户会以为是必填 */}
              {!open && (
                <span className='text-xs text-gray-400 truncate min-w-0'>
                  {value || t('留空则由模型自己配')}
                </span>
              )}
            </button>
            {open && (
              <div className='px-3 pb-2'>
                <TextArea
                  value={value}
                  onChange={(v) => onChange(f.key, v)}
                  placeholder={t(f.placeholder)}
                  disabled={disabled}
                  autosize={{ minRows: 2, maxRows: 5 }}
                  className='!rounded-lg'
                />
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

// 输入框上方那行「画面描述」小标题。单独导出而不是塞进上面的组件里:它必须紧贴提示词
// 输入框,而输入框在 VideoChatArea 里(带发送按钮与回车发送,不宜搬家)。
export const H3MainFieldLabel = () => {
  const { t } = useTranslation();
  return (
    <Typography.Text
      type='tertiary'
      className='text-xs font-medium block mb-1 !text-gray-700'
    >
      {t('画面描述')}
    </Typography.Text>
  );
};

export default H3PromptFields;
