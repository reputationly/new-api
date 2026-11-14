/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useRef } from 'react';
import { Modal, Form, Button } from '@douyinfe/semi-ui';
import { TIME_PRESETS } from '../../../helpers/dashboard';

const SearchModal = ({
  searchModalVisible,
  handleSearchConfirm,
  handleCloseModal,
  isMobile,
  isAdminUser,
  inputs,
  dataExportDefaultTime,
  timeOptions,
  handleInputChange,
  applyPreset,
  activePreset,
  t,
}) => {
  const formRef = useRef();
  // 快捷范围要同时改「表单显示值」和 hook 里的 inputs。只改后者的话，Semi 的表单字段
  // 仍然显示自己内部的值（withField 把 [valueKey]: value 放在 ...rest 之后，传进来的
  // value 会被内部值覆盖），用户看到的和实际查的就不是同一个区间。
  const formApiRef = useRef();

  const FORM_FIELD_PROPS = {
    className: 'w-full mb-2 !rounded-lg',
  };

  const createFormField = (Component, props) => (
    <Component {...FORM_FIELD_PROPS} {...props} />
  );

  const { start_timestamp, end_timestamp, username } = inputs;

  const handlePresetClick = (key) => {
    // 直接用 applyPreset 算好的那份区间，不再自己取一次——「今天 / 近24小时」的
    // 结束时间来自「现在」，两次取现在跨秒就会让显示值和查询值差 1 秒。
    const range = applyPreset(key);
    if (!range) {
      return;
    }
    formApiRef.current?.setValues({
      start_timestamp: range.start_timestamp,
      end_timestamp: range.end_timestamp,
      data_export_default_time: range.granularity,
    });
  };

  return (
    <Modal
      title={t('搜索条件')}
      visible={searchModalVisible}
      onOk={handleSearchConfirm}
      onCancel={handleCloseModal}
      closeOnEsc={true}
      size={isMobile ? 'full-width' : 'small'}
      centered
    >
      <Form
        ref={formRef}
        getFormApi={(api) => (formApiRef.current = api)}
        layout='vertical'
        className='w-full'
      >
        {/* 快捷范围：点一下同时填起止时间并按跨度切粒度。
            没有这一行时，用户会以为下面的「图表粒度」就是时间范围（选「周」
            然后被 7 天窗口按周聚合，图里只剩一两个点）。 */}
        <div className='flex flex-wrap gap-2 mb-4'>
          {TIME_PRESETS.map((preset) => (
            <Button
              key={preset.key}
              size='small'
              theme={activePreset === preset.key ? 'solid' : 'borderless'}
              type={activePreset === preset.key ? 'primary' : 'tertiary'}
              onClick={() => handlePresetClick(preset.key)}
            >
              {t(preset.label)}
            </Button>
          ))}
          <Button
            key='custom'
            size='small'
            theme={activePreset ? 'borderless' : 'solid'}
            type={activePreset ? 'tertiary' : 'primary'}
            onClick={() => handlePresetClick('custom')}
          >
            {t('自定义')}
          </Button>
        </div>

        {createFormField(Form.DatePicker, {
          field: 'start_timestamp',
          label: t('起始时间'),
          initValue: start_timestamp,
          type: 'dateTime',
          name: 'start_timestamp',
          onChange: (value) => handleInputChange(value, 'start_timestamp'),
        })}

        {createFormField(Form.DatePicker, {
          field: 'end_timestamp',
          label: t('结束时间'),
          initValue: end_timestamp,
          type: 'dateTime',
          name: 'end_timestamp',
          onChange: (value) => handleInputChange(value, 'end_timestamp'),
        })}

        {/* 「粒度」只影响图表横轴分桶，不改变统计范围。原名「时间粒度」正是
            「7 天范围 + 周粒度」这种误解的来源。 */}
        {createFormField(Form.Select, {
          field: 'data_export_default_time',
          label: t('图表粒度'),
          extraText: t('仅影响图表横轴聚合，不影响统计范围'),
          initValue: dataExportDefaultTime,
          placeholder: t('图表粒度'),
          name: 'data_export_default_time',
          optionList: timeOptions,
          onChange: (value) =>
            handleInputChange(value, 'data_export_default_time'),
        })}

        {isAdminUser &&
          createFormField(Form.Input, {
            field: 'username',
            label: t('用户名称'),
            initValue: username,
            value: username,
            placeholder: t('可选值'),
            name: 'username',
            onChange: (value) => handleInputChange(value, 'username'),
          })}
      </Form>
    </Modal>
  );
};

export default SearchModal;
