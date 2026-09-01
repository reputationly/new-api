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

import React, { useState } from 'react';
import {
  Avatar,
  Typography,
  Card,
  Input,
  Badge,
  Button,
  Modal,
  Space,
} from '@douyinfe/semi-ui';
import { QRCodeSVG } from 'qrcode.react';
import { Copy, Users, UserCheck, Coins, Gift, QrCode } from 'lucide-react';
import { quotaToPoints } from '../../helpers/quota';
import InvitedUsersTable from './InvitedUsersTable';

const { Text } = Typography;

const InvitationCard = ({ t, userState, affLink, handleAffLinkClick }) => {
  // 被邀请人列表的汇总统计（总数 / 已实名数），由内嵌表格加载后回传。
  const [stats, setStats] = useState({ total: 0, verifiedTotal: 0 });
  const [qrVisible, setQrVisible] = useState(false);

  const inviteCount = stats.total || userState?.user?.aff_count || 0;
  const verifiedCount = stats.verifiedTotal || 0;
  const pointsEarned = quotaToPoints(userState?.user?.aff_points_earned || 0);

  const statItems = [
    { icon: Users, label: t('邀请人数'), value: inviteCount },
    { icon: UserCheck, label: t('已实名人数'), value: verifiedCount },
    { icon: Coins, label: t('累计邀请积分'), value: pointsEarned },
  ];

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      {/* 卡片头部 */}
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='green' className='mr-3 shadow-md'>
          <Gift size={16} />
        </Avatar>
        <div>
          <Typography.Text className='text-lg font-medium'>
            {t('邀请奖励')}
          </Typography.Text>
          <div className='text-xs'>{t('邀请好友获得积分奖励')}</div>
        </div>
      </div>

      <Space vertical style={{ width: '100%' }}>
        {/* 统计数据统一卡片 */}
        <Card
          className='!rounded-xl w-full'
          cover={
            <div
              className='relative h-30'
              style={{
                '--palette-primary-darkerChannel': '0 75 80',
                backgroundImage: `linear-gradient(0deg, rgba(var(--palette-primary-darkerChannel) / 80%), rgba(var(--palette-primary-darkerChannel) / 80%)), url('/cover-4.webp')`,
                backgroundSize: 'cover',
                backgroundPosition: 'center',
                backgroundRepeat: 'no-repeat',
              }}
            >
              <div className='relative z-10 h-full flex flex-col justify-between p-4'>
                <div className='flex justify-between items-center'>
                  <Text strong style={{ color: 'white', fontSize: '16px' }}>
                    {t('邀请统计')}
                  </Text>
                </div>

                {/* 统计数据 */}
                <div className='grid grid-cols-3 gap-6 mt-4'>
                  {statItems.map(({ icon: Icon, label, value }) => (
                    <div key={label} className='text-center'>
                      <div
                        className='text-base sm:text-2xl font-bold mb-2'
                        style={{ color: 'white' }}
                      >
                        {value}
                      </div>
                      <div className='flex items-center justify-center text-sm'>
                        <Icon
                          size={14}
                          className='mr-1'
                          style={{ color: 'rgba(255,255,255,0.8)' }}
                        />
                        <Text
                          style={{
                            color: 'rgba(255,255,255,0.8)',
                            fontSize: '12px',
                          }}
                        >
                          {label}
                        </Text>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          }
        >
          {/* 邀请链接部分 */}
          <Input
            value={affLink}
            readonly
            className='!rounded-lg'
            prefix={t('邀请链接')}
            suffix={
              // 两个按钮包一层带右间距的容器：Semi 的 suffix 是紧贴输入框右边框的，
              // 复制按钮原本几乎顶着边，再塞一个二维码按钮会更挤。
              <Space spacing={4} className='mr-1'>
                <Button
                  theme='borderless'
                  onClick={() => setQrVisible(true)}
                  icon={<QrCode size={14} />}
                  className='!rounded-lg'
                  disabled={!affLink}
                >
                  {t('二维码')}
                </Button>
                <Button
                  type='primary'
                  theme='solid'
                  onClick={handleAffLinkClick}
                  icon={<Copy size={14} />}
                  className='!rounded-lg'
                >
                  {t('复制')}
                </Button>
              </Space>
            }
          />
        </Card>

        {/* 奖励说明 */}
        <Card
          className='!rounded-xl w-full'
          title={<Text type='tertiary'>{t('奖励说明')}</Text>}
        >
          <div className='space-y-3'>
            <div className='flex items-start gap-2'>
              <Badge dot type='success' />
              <Text type='tertiary' className='text-sm'>
                {t('邀请好友注册，好友完成实名认证后您可获得积分奖励')}
              </Text>
            </div>

            <div className='flex items-start gap-2'>
              <Badge dot type='success' />
              <Text type='tertiary' className='text-sm'>
                {t('获得的积分可用于消费抵扣')}
              </Text>
            </div>

            <div className='flex items-start gap-2'>
              <Badge dot type='success' />
              <Text type='tertiary' className='text-sm'>
                {t('邀请的好友越多，获得的积分越多')}
              </Text>
            </div>
          </div>
        </Card>

        {/* 我邀请的用户列表 */}
        <Card
          className='!rounded-xl w-full'
          title={<Text type='tertiary'>{t('我邀请的用户')}</Text>}
        >
          <InvitedUsersTable t={t} onStats={setStats} />
        </Card>
      </Space>

      <Modal
        title={t('邀请二维码')}
        visible={qrVisible}
        onCancel={() => setQrVisible(false)}
        footer={null}
        centered
        width={320}
      >
        <div className='flex flex-col items-center gap-3 py-2'>
          {/*
            白底留边是二维码能被扫出来的前提：深色模式下容器背景是深的，
            SVG 的浅色模块会和背景糊在一起，多数扫码器直接识别不了。
          */}
          <div className='rounded-lg bg-white p-3'>
            <QRCodeSVG value={affLink || ''} size={200} />
          </div>
          <Text type='tertiary' size='small' className='text-center'>
            {t('好友扫码注册即计入你的邀请')}
          </Text>
          <Text
            type='tertiary'
            size='small'
            className='break-all text-center opacity-70'
          >
            {affLink}
          </Text>
        </div>
      </Modal>
    </Card>
  );
};

export default InvitationCard;
