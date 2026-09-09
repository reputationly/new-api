import React from 'react';
import { Button, Form } from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';

import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';
import {
  MODERATION_ACTIONS,
  MODERATION_CATEGORIES,
  MODERATION_SOURCES,
} from '../../../constants/moderation.constants';

const ModerationLogsFilters = ({
  formInitValues,
  setFormApi,
  refresh,
  formApi,
  loading,
  t,
}) => {
  return (
    <Form
      initValues={formInitValues}
      getFormApi={(api) => setFormApi(api)}
      onSubmit={refresh}
      allowEmpty={true}
      autoComplete='off'
      layout='vertical'
      trigger='change'
      stopValidateWithError={false}
    >
      <div className='flex flex-col gap-2'>
        <div className='grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2'>
          <div className='col-span-1 lg:col-span-2'>
            <Form.DatePicker
              field='dateRange'
              className='w-full'
              type='dateTimeRange'
              placeholder={[t('开始时间'), t('结束时间')]}
              showClear
              pure
              size='small'
              presets={DATE_RANGE_PRESETS.map((preset) => ({
                text: t(preset.text),
                start: preset.start(),
                end: preset.end(),
              }))}
            />
          </div>

          <Form.Select
            field='action'
            placeholder={t('全部处置')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            {MODERATION_ACTIONS.map((a) => (
              <Form.Select.Option key={a.value} value={a.value}>
                {t(a.label)}
              </Form.Select.Option>
            ))}
          </Form.Select>

          <Form.Select
            field='category'
            placeholder={t('全部类别')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            {MODERATION_CATEGORIES.map((c) => (
              <Form.Select.Option key={c.value} value={c.value}>
                {t(c.label)}
              </Form.Select.Option>
            ))}
          </Form.Select>

          <Form.Input
            field='word'
            prefix={<IconSearch />}
            placeholder={t('命中词')}
            showClear
            pure
            size='small'
          />

          <Form.Input
            field='username'
            prefix={<IconSearch />}
            placeholder={t('用户名')}
            showClear
            pure
            size='small'
          />

          <Form.Input
            field='model_name'
            prefix={<IconSearch />}
            placeholder={t('模型名称')}
            showClear
            pure
            size='small'
          />

          <Form.Input
            field='group'
            prefix={<IconSearch />}
            placeholder={t('分组')}
            showClear
            pure
            size='small'
          />

          <Form.Select
            field='source'
            placeholder={t('全部来源')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            {MODERATION_SOURCES.map((s) => (
              <Form.Select.Option key={s.value} value={s.value}>
                {t(s.label)}
              </Form.Select.Option>
            ))}
          </Form.Select>

          <Form.Input
            field='channel_id'
            prefix={<IconSearch />}
            placeholder={t('渠道 ID')}
            showClear
            pure
            size='small'
          />

          <Form.Input
            field='request_id'
            prefix={<IconSearch />}
            placeholder={t('请求 ID')}
            showClear
            pure
            size='small'
          />
        </div>

        <div className='flex justify-between items-center'>
          <div></div>
          <div className='flex gap-2'>
            <Button
              type='tertiary'
              htmlType='submit'
              loading={loading}
              size='small'
            >
              {t('查询')}
            </Button>
            <Button
              type='tertiary'
              onClick={() => {
                if (formApi) {
                  formApi.reset();
                  // 重置后立即查询，用 setTimeout 确保表单重置已完成
                  setTimeout(() => {
                    refresh();
                  }, 100);
                }
              }}
              size='small'
            >
              {t('重置')}
            </Button>
          </div>
        </div>
      </div>
    </Form>
  );
};

export default ModerationLogsFilters;
