import React, { useState } from 'react';
import { Picker } from 'antd-mobile';
import { DownOutline } from 'antd-mobile-icons';

// 生成页顶部的参数胶囊条：每个字段一个可点 Tag，点开 antd-mobile Picker 单列选择。
// options 支持字符串数组或 {label, value} 数组。
const normalizeOptions = (options = []) =>
  options.map((o) =>
    typeof o === 'object' && o !== null
      ? { label: String(o.label ?? o.value), value: o.value }
      : { label: String(o), value: o },
  );

const ConfigBar = ({ fields, disabled = false }) => {
  const [openKey, setOpenKey] = useState('');

  return (
    <div className='m-config-bar'>
      {fields
        .filter((f) => (f.options || []).length > 0)
        .map((f) => {
          const opts = normalizeOptions(f.options);
          const current = opts.find((o) => o.value === f.value);
          // 只有一个候选值时收成静态标签：没什么可挑的，不必摆出可点的假象。仍然渲染而
          // 不是隐藏——分组决定倍率与可用渠道，藏起来会让计费不透明。
          const single = opts.length <= 1;
          return (
            <React.Fragment key={f.key}>
              <div
                className={`m-config-chip${current ? ' active' : ''}${single ? ' static' : ''}`}
                onClick={() => !disabled && !single && setOpenKey(f.key)}
              >
                <span className='m-chip-key'>
                  {f.label}
                  {current ? '：' : ''}
                </span>
                {current && (
                  // 值单独截断：模型名长短不一，不给它设上限的话胶囊条会跟着换行，
                  // 整个配置区高度随选中的模型跳变，下面的对话区也跟着忽高忽矮。
                  // 全名在 Picker 里本来就看得到，这里截断不丢信息。
                  <span className='m-chip-val'>{current.label}</span>
                )}
                {!single && <DownOutline fontSize={9} />}
              </div>
              {!single && (
                <Picker
                  columns={[opts]}
                  visible={openKey === f.key}
                  value={[f.value]}
                  onClose={() => setOpenKey('')}
                  onConfirm={(v) => f.onChange(v[0])}
                />
              )}
            </React.Fragment>
          );
        })}
    </div>
  );
};

export default ConfigBar;
