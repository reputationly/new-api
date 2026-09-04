import React from 'react';
import { Select } from '@douyinfe/semi-ui';

// 「可选可填」的下拉:Semi Select 的 filter + allowCreate,外加一个绕过 Semi 自身 bug 的 key。
//
// Semi 2.72 的 Select 在 **allowCreate + 受控 value** 下,optionList 变化时不重新收集候选:
// foundation.handleValueChange 对这个组合走的是 getState('options') 的旧快照
// (源码注释原话 "AllowCreate and controlled mode, no need to re-collect optionList"),
// 而 componentDidUpdate 里 handleOptionListChange 的 setState 还没落地。后果是候选只要
// 在挂载**之后**才到(接口异步返回、或随另一个下拉的选择而变),下拉永远「暂无数据」——
// 体验区管理的「优化用的分组 / 语言模型」就是这样空的,接口明明 200 返回了数组。
// 复现条件必须同时满足 filter、allowCreate、props 里有 value(哪怕是 undefined);
// 去掉任一项都正常,remote / autoClearSearchValue 都绕不过。
//
// 绕法:拿候选的 value 序列当 key,候选一变就整个重挂,Semi 重新走 init 收集一遍。
// 代价是重挂时丢掉输入框里没提交的搜索词;候选只在接口返回、或上游下拉切换时变,
// 这两种时刻用户不在这个框里打字,可以接受。
// 别用在 multiple 且候选来自 value 本身的场景(FieldInput 的列表字段):每选一项
// 候选就变、就重挂、下拉就合上,连续录入会被打断。
const optionsKey = (optionList) =>
  (optionList || []).map((o) => String(o?.value)).join(' ');

const CreatableSelect = (props) => (
  <Select key={optionsKey(props.optionList)} filter allowCreate {...props} />
);

export default CreatableSelect;
