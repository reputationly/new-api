import React, { useMemo } from 'react';
import { Typography } from '@douyinfe/semi-ui';
import { Image as ImageIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { sizeToRatio } from '../../constants/imagePlayground.constants';

// 画幅选择器：图片体验区（文生图/图生图）与视频体验区（文生视频/关键帧/参考生视频）
// 共用这一份实现 —— 原来是图片侧一排图形按钮、视频侧一个下拉，两边风格各飘一套。
//
// 固定 6 列折行。改造前图片侧是 `repeat(N, 1fr)` 的**单排**：面板可用宽 244px
// （卡片 300 - 内边距 24×2 - 滚动条位 8），配 11 个比例时每格只剩 ~17px，比里面
// 20px 的比例方块还窄，方块直接溢出格子 —— 那不是审美问题，是格子比内容窄。
// 6 列后每格 ~36px，方块与 10px 标签才装得下；11 个落成 6+5，第二行末尾空一格。
const COLUMNS = 6;
// 比例方块的长边像素。按比例缩短边，横/竖/方一眼可辨 —— 比一串 "16:9 / 9:16"
// 文字快得多，尤其横竖两版只差一个冒号的位置。
const BOX_MAX = 20;

const glyphSize = (r) => {
  const v = sizeToRatio(r) || 1;
  return v >= 1
    ? { width: BOX_MAX, height: Math.max(5, Math.round(BOX_MAX / v)) }
    : { width: Math.max(5, Math.round(BOX_MAX * v)), height: BOX_MAX };
};

// 显示顺序：宽 → 方 → 竖（比例降序）。
//
// 运营在配置里是按「一对横竖挨着」的习惯录入的（16:9、9:16、1:1、4:3、3:4…），
// 折成两行后配对必然被行断开（21:9 掉到第一行末尾、9:21 落到第二行开头）。按比例
// 排序后第一行全是横版、第二行是方版与竖版，扫一眼就知道「要竖的往下看」；而且
// 阅读方向上是单调递减，眼睛不用来回折返。**只是显示层排序，配置集合不动**。
//
// 「跟随上传素材」不是比例、解析不出数值，固定钉在第一格（它是关键帧的默认项）。
const orderChoices = (choices, followValue) => {
  const follow =
    followValue && choices.includes(followValue) ? followValue : null;
  const rest = choices
    .filter((c) => c !== follow)
    .map((v, i) => ({ v, i, r: sizeToRatio(v) }));
  rest.sort((a, b) => {
    // 解析不出比例的（运营录入的自定义写法）排最后，彼此保持配置顺序
    if (a.r == null || b.r == null) {
      if (a.r == null && b.r == null) return a.i - b.i;
      return a.r == null ? 1 : -1;
    }
    return b.r !== a.r ? b.r - a.r : a.i - b.i;
  });
  const ordered = rest.map((x) => x.v);
  return follow ? [follow, ...ordered] : ordered;
};

const RatioPicker = ({
  choices = [],
  value,
  onChange,
  disabled = false,
  // 「跟随上传素材」档的档位值（视频侧是 VIDEO_ASPECT_RATIO_AUTO）。不传就是纯比例组。
  followValue,
  // 该档的标签。它是**模式**不是比例（选中即完全不干预画幅、也不下发比例字段），
  // 所以图标与标签都另做一套，不能画成比例方块 —— 中文短词才能在 36px 的格子里放下。
  followLabel,
  // 选中该档时的说明：光看标签说不出它的代价（不裁图、不加边、画幅就是原图）。
  followHint,
}) => {
  const { t } = useTranslation();
  const list = useMemo(
    () => orderChoices(choices, followValue),
    [choices, followValue],
  );
  const followText = followLabel ?? t('跟随');
  // 只配一两个比例时仍然铺满整行（与改造前一致）；到 6 个以上才固定 6 列折行，
  // 否则「只有一个 1:1」的模型会得到一个孤零零的小方块。
  const columns = Math.min(COLUMNS, Math.max(list.length, 1));

  return (
    <>
      <div
        className='grid gap-1.5'
        style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
      >
        {list.map((r) => {
          const active = r === value;
          const isFollow = r === followValue;
          return (
            <button
              key={r}
              type='button'
              disabled={disabled}
              aria-pressed={active}
              title={isFollow ? followText : r}
              onClick={() => onChange(r)}
              // 颜色只能用 semi-color-* 这套:classic 的 tailwind.config.js 把
              // theme.colors 整份替换成了 semi 变量,blue/gray/white 这些默认色板
              // 在构建产物里根本不存在 —— 之前的 bg-blue-50 / border-blue-500 从来
              // 没生效过,所以「点了没有选中效果」。
              className={`flex min-w-0 flex-col items-center justify-center gap-1 rounded-md border px-0 py-1.5 transition-colors ${
                active
                  ? 'border-semi-color-primary bg-semi-color-primary text-semi-color-white shadow-sm'
                  : 'border-semi-color-border text-semi-color-text-2 hover:border-semi-color-primary hover:bg-semi-color-primary-light-default'
              } ${disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'}`}
            >
              {/* 图标区固定 20px 高:比例方块按比例缩，高度各不相同，不固定的话
                  同一行里的文字标签会一高一低对不齐。 */}
              <span
                className='flex items-center justify-center'
                style={{ height: BOX_MAX }}
              >
                {isFollow ? (
                  <ImageIcon size={16} />
                ) : (
                  <span
                    className={`block rounded-[2px] border ${
                      active
                        ? 'border-semi-color-white'
                        : 'border-semi-color-text-3'
                    }`}
                    style={glyphSize(r)}
                  />
                )}
              </span>
              <span className='block max-w-full truncate text-[10px] font-medium leading-none tracking-tight'>
                {isFollow ? followText : r}
              </span>
            </button>
          );
        })}
      </div>
      {!!followHint && value === followValue && (
        <Typography.Text type='tertiary' size='small' className='block mt-1'>
          {followHint}
        </Typography.Text>
      )}
    </>
  );
};

export default RatioPicker;
