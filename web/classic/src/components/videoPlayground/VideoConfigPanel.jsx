import React from 'react';
import {
  Card,
  Select,
  Switch,
  Typography,
  Tooltip,
  InputNumber,
} from '@douyinfe/semi-ui';
import {
  Settings,
  Users,
  Sparkles,
  Ruler,
  Clock,
  HelpCircle,
  Shuffle,
  Proportions,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { renderGroupOption, selectFilter } from '../../helpers';
import ImageUrlInput from '../playground/ImageUrlInput';
import MediaFileInput from './MediaFileInput';
import { tabHasField } from '../../constants/playgroundAdmin.constants';
import { imageConstraintsForMode } from '../../constants/videoPlayground.constants';

const VideoConfigPanel = ({
  needsImage = false,
  // 哪些控件出现在本 tab,统一由中央元数据的 fields 声明决定(见 playgroundAdmin.
  // constants.js)—— 以前这里按 isSR/isDub/isS2V/followsInput 各写一套 if,与 admin
  // 页、手机端各自的判断分了三家,加一个玩法要改三处且容易漏。
  category = 'video',
  mode = 'text2video',
  isI2V = false,
  isR2VA = false,
  isFLF2V = false,
  isS2V = false,
  isSR = false,
  isDub = false,
  isVACE = false,
  // 关键帧 tab 里 i2v / flf2v 两类模型共存,尾帧是否可传完全由所选模型决定。判断在
  // useVideoGeneration 里做(要读运营配置里的 taskType 声明),这里只消费结果。
  isFlf2vSelected = false,
  allowLastFrame = false,
  isKeyframeAuto = false,
  isKeyframeAutoFull = false,
  dubAvailable = false,
  // 选中模型是否跑在自建 gpustackplus 引擎上：1080P 两段流水线与插帧都只对它成立，
  // 其余渠道原样透传，故那两处 UI 也只对它展示（见 useVideoGeneration 的 pipelineModel）。
  pipelineModel = false,
  maxRefImages = 5,
  // 参考视频三项各自可配(见 playgroundAdmin 的 FIELD_META)。
  // maxRefVideos = 0 是**默认且有意义的取值**:运营没开放这个模态,上传框整个不渲染
  // —— 与 sizes/aspectRatios 留空即不展示选择器同一套「纯 opt-in」风格。
  maxRefVideos = 0,
  refVideoMaxMB = 0,
  refVideoMaxSec = 0,
  maxInputMB = 0,
  maxAudioSec = 0,
  inputs,
  groups,
  models,
  availableSizes,
  availableDurations,
  availableAspectRatios,
  onInputChange,
  disabled = false,
  styleState,
}) => {
  const { t } = useTranslation();

  // 尾帧槽:flf2v 必填、auto(H3 这类单 checkpoint 全能模型)可选、i2v 不渲染。
  const needsLastFrame = allowLastFrame;

  // 参考视频这一模态是否开放。0 不是「不限」而是「不开放」——与 maxInputMB 的
  // 「0=不限」相反,这两类字段的 0 语义必须分清:数量闸的 0 关掉整个控件,
  // 体积/时长闸的 0 才是不限。
  const refVideosOpen = isR2VA && maxRefVideos > 0;
  // 槽位按下标定位,中间允许留空(用户可能先传第 2 个)。提交侧统一 filter(Boolean),
  // 所以这里不必压紧数组——压紧会让正在编辑的槽位跳位。
  const setRefVideoAt = (i, v) => {
    const next = Array.from(
      { length: maxRefVideos },
      (_, j) => (inputs.refVideos || [])[j] || '',
    );
    next[i] = v || '';
    onInputChange('refVideos', next);
  };
  // auto(全态,H3):首尾两槽都可选,至少填一个 ——「只给尾帧」是合法玩法。
  // auto_fl(Seedance):首帧必填、尾帧可选 —— 它不支持仅尾帧。
  const firstFrameOptional = isKeyframeAutoFull;
  const lastFrameOptional = isKeyframeAuto;

  // 输入大小上限(MB):直接透传 maxInputMB。0/未配 = 不限(与配置页「留空/0 不限」及
  // 后端一致);>0 时各上传控件按它拦。不再套前端兜底默认,避免和「显式不限」冲突。
  const uploadMaxMB = maxInputMB;
  // 参考图像素约束按 tab 取(多模型共享的 tab 用最小交集),见常量处的说明。
  const imageConstraints = imageConstraintsForMode(mode);

  // 驱动音频时长上限(秒),同样 0/未配 = 不限。只作用于数字人的音频槽:该任务的产出
  // 长度就是音频长度,这是唯一需要按时长兜成本的输入。
  const uploadMaxAudioSec = maxAudioSec;

  // 单帧上传槽:ImageUrlInput 管理数组,这里只取最后一张作为该槽的单帧。
  // 帧图仅在 i2v/flf2v 模式渲染,均为必填 → 单行标签(上传首帧/尾帧)+ 红星,无启用开关。
  // 只读图片预览(锁定/历史态)。ImageUrlInput 的图片列表在 disabled 时会被整体隐藏
  // (isActive = imageEnabled && !disabled),不能用来做只读展示;锁定态改用纯 <img>。
  const renderImagePreview = (label, urls) => (
    <div>
      <div className='flex items-center gap-1 mb-2'>
        <Typography.Text strong className='text-sm'>
          {label}
        </Typography.Text>
      </div>
      <div className='flex flex-wrap gap-2'>
        {(urls || []).filter(Boolean).map((url, i) => (
          <img
            key={i}
            src={url}
            alt={`${label}-${i + 1}`}
            className='w-20 h-20 object-cover rounded-lg border border-gray-200'
          />
        ))}
      </div>
    </div>
  );

  // 单帧槽:未锁定=可编辑上传(ImageUrlInput);锁定/历史=只读预览已上传帧。
  const renderFrameSlot = (label, key, opts = {}) =>
    disabled ? (
      inputs[key] ? (
        renderImagePreview(label, [inputs[key]])
      ) : null
    ) : (
      <ImageUrlInput
        label={label}
        required={!opts.optional}
        maxMB={uploadMaxMB}
        {...imageConstraints}
        maxCount={1}
        imageUrls={inputs[key] ? [inputs[key]] : []}
        onImageUrlsChange={(v) =>
          onInputChange(key, (v && v.length ? v[v.length - 1] : '') || '')
        }
        disabled={false}
      />
    );

  const ensureOption = (options, value) => {
    if (!value) return options;
    return options.some((o) => o.value === value)
      ? options
      : [...options, { label: value, value }];
  };

  const groupOptions = ensureOption(groups || [], inputs.group);
  const modelOptions = ensureOption(models || [], inputs.model);
  const sizeOptions = ensureOption(
    (availableSizes || []).map((s) => ({ label: s, value: s })),
    inputs.size,
  );
  const durationOptions = ensureOption(
    (availableDurations || []).map((s) => ({ label: `${s}s`, value: s })),
    inputs.seconds,
  );
  const aspectRatioOptions = ensureOption(
    (availableAspectRatios || []).map((r) => ({ label: r, value: r })),
    inputs.aspectRatio,
  );

  return (
    <Card
      className='h-full flex flex-col'
      bordered={false}
      bodyStyle={{
        padding: styleState?.isMobile ? '16px' : '24px',
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      <div className='flex items-center mb-6 flex-shrink-0'>
        <div className='w-10 h-10 rounded-full bg-gradient-to-r from-purple-500 to-pink-500 flex items-center justify-center mr-3'>
          <Settings size={20} className='text-white' />
        </div>
        <Typography.Title heading={5} className='mb-0'>
          {t('模型配置')}
        </Typography.Title>
      </div>

      <div className='space-y-6 overflow-y-auto flex-1 pr-2'>
        {/* 分组 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Users size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('分组')}
            </Typography.Text>
            <Tooltip
              content={t('仅展示包含视频生成模型的分组。')}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            placeholder={t('请选择分组')}
            name='group'
            required
            selection
            filter={selectFilter}
            autoClearSearchValue={false}
            onChange={(value) => onInputChange('group', value)}
            value={inputs.group}
            optionList={groupOptions}
            renderOptionItem={renderGroupOption}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 模型 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Sparkles size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('模型')}
            </Typography.Text>
            <Tooltip
              content={t('仅展示具备视频生成能力的模型。')}
              position='top'
            >
              <HelpCircle size={14} className='text-gray-400 cursor-help' />
            </Tooltip>
          </div>
          <Select
            placeholder={t('请选择模型')}
            name='model'
            required
            selection
            filter={selectFilter}
            autoClearSearchValue={false}
            onChange={(value) => onInputChange('model', value)}
            value={inputs.model}
            optionList={modelOptions}
            emptyContent={t('当前分组下暂无视频模型')}
            disabled={disabled}
            style={{ width: '100%' }}
            dropdownStyle={{ width: '100%', maxWidth: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 主图上传(图生视频:首帧;首尾帧:首帧+尾帧;数字人:人物图)。锁定/历史态改为
            只读展示已上传的文件(disabled 透传,ImageUrlInput/MediaFileInput 仍渲染预览/播放器);
            未上传的可选项在锁定态不展示,避免空的禁用上传框。 */}
        {needsImage &&
          (!disabled || inputs.firstFrame) &&
          renderFrameSlot(
            isS2V
              ? t('上传人物图')
              : isFLF2V
                ? t('上传首帧')
                : t('上传首帧/参考图'),
            'firstFrame',
            { optional: isFLF2V && firstFrameOptional },
          )}
        {/* 关键帧尾帧槽,三态(见 keyframeModeOf):
            - flf2v:渲染且必填;
            - auto :渲染但可选 —— 一个 checkpoint 同时吃首帧/尾帧/首尾帧,只给尾帧
                    (l2va)由引擎按 frame_indices=[-1] 反推开头;
            - i2v  :压根不给这个框 —— 它的引擎实例会静默丢弃尾帧,给了框等于骗用户。 */}
        {isFLF2V &&
          needsLastFrame &&
          (!disabled || inputs.lastFrame) &&
          renderFrameSlot(t('上传尾帧'), 'lastFrame', {
            optional: lastFrameOptional,
          })}

        {/* 参考生视频:音色参考(可选)。与数字人的「驱动音频」是两回事 ——
            这段音频只提供音色/说话风格,长度与输出时长无关;要说什么写在提示词里。 */}
        {isR2VA && (!disabled || inputs.audioData) && (
          <MediaFileInput
            label={t('上传音色参考（可选）')}
            kind='audio'
            maxMB={uploadMaxMB}
            maxSec={uploadMaxAudioSec}
            value={inputs.audioData}
            onChange={(v) => onInputChange('audioData', v)}
            disabled={disabled}
          />
        )}

        {/* 数字人:驱动音频(必填) */}
        {isS2V && (!disabled || inputs.audioData) && (
          <MediaFileInput
            label={t('上传驱动音频')}
            required
            kind='audio'
            maxMB={uploadMaxMB}
            maxSec={uploadMaxAudioSec}
            value={inputs.audioData}
            disabled={disabled}
            onChange={(v) => onInputChange('audioData', v)}
          />
        )}

        {/* 视频超分:源视频(必填)+ 超分倍率;视频配乐:仅源视频(必填,复用同一输入)。 */}
        {(isSR || isDub) && (!disabled || inputs.sourceVideo) && (
          <>
            <MediaFileInput
              label={t(isDub ? '上传待配音视频' : '上传源视频')}
              required
              kind='video'
              value={inputs.sourceVideo}
              maxMB={uploadMaxMB}
              disabled={disabled}
              onChange={(v) => onInputChange('sourceVideo', v)}
            />
            {isSR && (
              <div>
                <div className='flex items-center gap-2 mb-2'>
                  <Ruler size={16} className='text-gray-500' />
                  <Typography.Text strong className='text-sm'>
                    {t('超分倍率')}
                  </Typography.Text>
                </div>
                <InputNumber
                  min={1}
                  max={4}
                  step={0.25}
                  value={inputs.srRatio}
                  onChange={(v) => onInputChange('srRatio', v == null ? 2 : v)}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  className='!rounded-lg'
                />
              </div>
            )}
          </>
        )}

        {/* 图生视频(Bernini r2v):参考图 1~3 张(必填),定义主体/服装/道具/场景。
            参考生视频(r2va):同一组控件,张数与是否展示都由运营配(默认 9 张)。
            运营把张数配成 0 = 这个玩法不收参考图,控件整个不渲染。 */}
        {(isI2V || isR2VA) && (
          <>
            {!disabled && maxRefImages > 0 && (
              <ImageUrlInput
                label={
                  // 开放了参考视频之后参考图就不再是「必填」——视觉参考图或视频至少
                  // 其一即可(与 hook 里的提交校验同一条判据)。标错会让用户以为
                  // 只传视频发不出去。
                  refVideosOpen
                    ? t('上传参考图（最多 {{count}} 张）', {
                        count: maxRefImages,
                      })
                    : t('上传参考图（必填，最多 {{count}} 张）', {
                        count: maxRefImages,
                      })
                }
                maxMB={uploadMaxMB}
                {...imageConstraints}
                maxCount={maxRefImages}
                imageUrls={inputs.refImages || []}
                onImageUrlsChange={(v) =>
                  onInputChange('refImages', (v || []).slice(0, maxRefImages))
                }
                disabled={false}
              />
            )}
            {/* 锁定态的只读预览**不受当前上限约束**:运营事后把张数调小(甚至调成 0),
                不该让历史会话里已经用过的素材凭空消失 —— 它们仍存在会话里、仍会随
                「重新生成」原样发出去,界面却不显示等于骗人。上限只管可编辑态。 */}
            {disabled &&
              (inputs.refImages || []).length > 0 &&
              renderImagePreview(t('参考图'), inputs.refImages)}
          </>
        )}

        {/* 参考生视频的参考视频:纯 opt-in —— 运营没配个数就整个不渲染。
            体积与时长走**参考视频自己的**上限(refVideoMaxMB / refVideoMaxSec),
            不跟参考图共用 maxInputMB:两者的合理体积差一个量级,共用一个旋钮必然
            有一边被误伤。 */}
        {/* 锁定态按会话里**实际存了几个**渲染,不按当前上限:上限调小之后,老会话里
            用过的第 2、3 个视频不该从界面上消失(理由同上面参考图那段)。 */}
        {isR2VA &&
          disabled &&
          (inputs.refVideos || [])
            .filter(Boolean)
            .map((val, i) => (
              <MediaFileInput
                key={`refVideo-view-${i}`}
                label={t('参考视频 {{n}}', { n: i + 1 })}
                kind='video'
                value={val}
                disabled
                onChange={() => {}}
              />
            ))}
        {refVideosOpen &&
          !disabled &&
          Array.from({ length: maxRefVideos }, (_, i) => (
            <MediaFileInput
              key={`refVideo-${i}`}
              label={
                maxRefVideos > 1
                  ? t('上传参考视频 {{n}}（可选，最多 {{count}} 个）', {
                      n: i + 1,
                      count: maxRefVideos,
                    })
                  : t('上传参考视频（可选）')
              }
              kind='video'
              maxMB={refVideoMaxMB}
              maxSec={refVideoMaxSec}
              value={(inputs.refVideos || [])[i] || ''}
              onChange={(v) => setRefVideoAt(i, v)}
            />
          ))}

        {/* 视频编辑(Bernini):1 源视频(必填),无参考图=v2v、带参考图=rv2v;
            仅参考图的 r2v 已迁到图生视频。
            体验区只给一个源视频口——双视频玩法(mv2v/ads2v)后端与门面仍全量支持,
            走 API 直连即可(与「参考生视频」同一套处理:体验区收窄、API 给全量)。 */}
        {isVACE && (
          <>
            {(!disabled || inputs.srcVideo) && (
              <MediaFileInput
                label={t('上传源视频')}
                required
                kind='video'
                value={inputs.srcVideo}
                maxMB={uploadMaxMB}
                disabled={disabled}
                onChange={(v) => onInputChange('srcVideo', v)}
              />
            )}
            {/* 第二源视频只在锁定态出现,且只有收口之前存下来的老会话才有值:它们仍会
                随续问/重新生成按 mv2v/ads2v 原样发出去,界面不显示就等于骗人(与参考图
                「上限只管可编辑态」同一条理由)。新会话恒为空,这里不会渲染。 */}
            {disabled && inputs.srcVideo2 && (
              <MediaFileInput
                label={t('第二视频')}
                kind='video'
                value={inputs.srcVideo2}
                disabled
                onChange={() => {}}
              />
            )}
            {!disabled && (
              <ImageUrlInput
                label={t('上传参考图（可选，最多 {{count}} 张）', {
                  count: maxRefImages,
                })}
                maxMB={uploadMaxMB}
                maxCount={maxRefImages}
                imageUrls={inputs.refImages || []}
                onImageUrlsChange={(v) =>
                  onInputChange('refImages', (v || []).slice(0, maxRefImages))
                }
                disabled={false}
              />
            )}
            {disabled &&
              (inputs.refImages || []).length > 0 &&
              renderImagePreview(t('参考图'), inputs.refImages)}
          </>
        )}

        {/* 视频尺寸/分辨率(仅文生视频,且该模型在后台配了尺寸才展示;图生视频跟随参考图) */}
        {tabHasField(category, mode, 'sizes') &&
          (availableSizes || []).length > 0 && (
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <Ruler size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('视频尺寸')}
                </Typography.Text>
              </div>
              <Select
                placeholder={t('请选择尺寸')}
                name='size'
                selection
                onChange={(value) => onInputChange('size', value)}
                value={inputs.size}
                optionList={sizeOptions}
                disabled={disabled}
                style={{ width: '100%' }}
                dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                className='!rounded-lg'
              />
              {pipelineModel && /1080/i.test(inputs.size || '') && (
                <Typography.Text className='text-xs text-amber-600 mt-1 block'>
                  {t(
                    '1080P 将先生成再调用超分模型提升画质：耗时更久，且会同时产生本模型与超分模型的额度/积分消耗',
                  )}
                </Typography.Text>
              )}
            </div>
          )}

        {/* 宽高比(仅文生视频,且该模型在后台配了宽高比才展示;wan 下由此决定输出分辨率) */}
        {tabHasField(category, mode, 'aspectRatios') &&
          (availableAspectRatios || []).length > 0 && (
            <div>
              <div className='flex items-center gap-2 mb-2'>
                <Proportions size={16} className='text-gray-500' />
                <Typography.Text strong className='text-sm'>
                  {t('宽高比')}
                </Typography.Text>
              </div>
              <Select
                placeholder={t('请选择宽高比')}
                name='aspectRatio'
                selection
                onChange={(value) => onInputChange('aspectRatio', value)}
                value={inputs.aspectRatio}
                optionList={aspectRatioOptions}
                disabled={disabled}
                style={{ width: '100%' }}
                dropdownStyle={{ width: '100%', maxWidth: '100%' }}
                className='!rounded-lg'
              />
            </div>
          )}

        {/* 时长(超分/配乐跟随源视频、数字人跟随驱动音频,均不展示)。
            数字人这条是实测结论:引擎不读 target_video_length,产出长度就是音频长度,
            摆个时长下拉只会骗人(选 5 秒拿到 10 秒)。长度管控走 maxAudioSec。 */}
        {tabHasField(category, mode, 'durations') && (
          <div>
            <div className='flex items-center gap-2 mb-2'>
              <Clock size={16} className='text-gray-500' />
              <Typography.Text strong className='text-sm'>
                {t('视频时长')}
              </Typography.Text>
            </div>
            <Select
              placeholder={t('请选择时长')}
              name='seconds'
              selection
              onChange={(value) => onInputChange('seconds', value)}
              value={inputs.seconds}
              optionList={durationOptions}
              disabled={disabled}
              style={{ width: '100%' }}
              dropdownStyle={{ width: '100%', maxWidth: '100%' }}
              className='!rounded-lg'
            />
          </div>
        )}

        {/* 随机种子(seed)—— 常驻,留空为随机 */}
        <div>
          <div className='flex items-center gap-2 mb-2'>
            <Shuffle size={16} className='text-gray-500' />
            <Typography.Text strong className='text-sm'>
              {t('随机种子')}
            </Typography.Text>
            <Typography.Text className='text-xs text-gray-400'>
              ({t('留空为随机')})
            </Typography.Text>
          </div>
          <InputNumber
            placeholder={t('留空为随机')}
            name='seed'
            min={0}
            precision={0}
            value={
              inputs.seed === '' || inputs.seed == null
                ? undefined
                : inputs.seed
            }
            onChange={(value) =>
              onInputChange('seed', value === '' || value == null ? '' : value)
            }
            disabled={disabled}
            style={{ width: '100%' }}
            className='!rounded-lg'
          />
        </div>

        {/* 插帧(RIFE 帧率翻倍)——自建引擎特有(target_fps),第三方渠道不展示;
            超分/配乐不适用;默认关,开启才透传 target_fps */}
        {pipelineModel && !isSR && !isDub && (
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <Typography.Text strong className='text-sm'>
                {t('插帧')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                ({t('帧率翻倍，生成更流畅')})
              </Typography.Text>
            </div>
            <Switch
              checked={!!inputs.interpolation}
              onChange={(v) => onInputChange('interpolation', v)}
              disabled={disabled}
              size='small'
            />
          </div>
        )}

        {/* 配音(v2a/LTX-2.3)——默认关;开启则生成后自动为成片配上 AI 音轨 */}
        {dubAvailable && (
          <div className='flex items-center justify-between'>
            <div className='flex items-center gap-2'>
              <Typography.Text strong className='text-sm'>
                {t('配音')}
              </Typography.Text>
              <Typography.Text className='text-xs text-gray-400'>
                ({t('生成后自动配音，耗时更久，额外消耗配音模型额度')})
              </Typography.Text>
            </div>
            <Switch
              checked={!!inputs.dubbing}
              onChange={(v) => onInputChange('dubbing', v)}
              disabled={disabled}
              size='small'
            />
          </div>
        )}
        {/* 配音不再有独立的提示词框：v2a 段直接复用生成这段视频的提示词
            （见 useVideoGeneration 里 pipeline.dub 的构造）。 */}
      </div>
    </Card>
  );
};

export default VideoConfigPanel;
