import React, { useEffect, useState, useContext } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Card, Typography, Button, Tag, Avatar } from '@douyinfe/semi-ui';
import { Gift, CalendarCheck, UserPlus, Ticket, Coins } from 'lucide-react';
import { API } from '../../helpers';
import { UserContext } from '../../context/User';

const { Text, Title } = Typography;

// §8ter 积分获取引导：任务清单式卡片，仅展示用户当前还能拿的项，完成即隐藏。
// 数据来自后端聚合接口 /api/user/points/overview（已按「完成即隐藏」下发任务）。
// 企业子账号不参与积分（获取渠道全被服务端封禁），整卡隐藏（后端 overview 同样有门）。
const PointsTasksCard = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [userState] = useContext(UserContext);
  const isSubAccount = (userState?.user?.parent_user_id || 0) > 0;
  const [data, setData] = useState(null);

  const loadOverview = async () => {
    try {
      const res = await API.get('/api/user/points/overview');
      if (res.data?.success) {
        setData(res.data.data);
      }
    } catch (e) {
      // 静默失败：引导卡片非关键路径，接口异常时整卡不显示
    }
  };

  useEffect(() => {
    if (!isSubAccount) {
      loadOverview();
    }
  }, [isSubAccount]);

  if (isSubAccount || !data || !data.enabled) return null;
  const tasks = data.tasks || [];
  if (tasks.length === 0) return null; // 空态：无可展示项则整卡隐藏

  const goKyc = () => navigate('/console/personal?tab=kyc');

  // 各任务类型的展示与 CTA。checkin 未实名时锁定，CTA 引导先实名。
  const renderTask = (task, idx) => {
    const locked = task.status === 'locked';
    let icon = <Coins size={16} />;
    let title = '';
    let desc = '';
    let pointsText = '';
    let ctaText = '';
    let onCta = () => {};
    let highlight = false;

    switch (task.type) {
      case 'kyc':
        icon = <Gift size={16} />;
        title = t('完成实名认证');
        desc = t('实名后可参与签到得分，并用积分抵扣消费');
        pointsText = `+${task.points}`;
        ctaText = t('去实名');
        onCta = goKyc;
        highlight = true; // 未实名置顶高亮（后端已把 kyc 放首位）
        break;
      case 'checkin':
        icon = <CalendarCheck size={16} />;
        title = t('每日签到');
        desc = locked
          ? t('实名认证后即可签到得积分')
          : t('每日签到可获得随机积分奖励');
        pointsText =
          task.points_max > task.points
            ? `+${task.points}~${task.points_max}`
            : `+${task.points_max || task.points}`;
        ctaText = locked ? t('去实名') : t('去签到');
        onCta = locked ? goKyc : () => navigate('/console/personal');
        break;
      case 'invite':
        icon = <UserPlus size={16} />;
        title = t('邀请好友实名');
        desc = t('每邀请一位好友完成实名，即可获得积分');
        pointsText = `+${task.points}`;
        ctaText = t('去邀请');
        onCta = () => navigate('/console/topup');
        break;
      case 'redemption':
        icon = <Ticket size={16} />;
        title = t('兑换积分码');
        desc = t('使用积分兑换码为账户充入积分');
        pointsText = '';
        ctaText = t('去兑换');
        onCta = () => navigate('/console/topup');
        break;
      default:
        return null;
    }

    return (
      <div
        key={idx}
        className={`flex items-center justify-between p-3 rounded-xl ${
          highlight
            ? 'bg-orange-50 dark:bg-orange-900/20 border border-orange-200 dark:border-orange-800'
            : 'bg-slate-50 dark:bg-slate-800'
        }`}
      >
        <div className='flex items-center min-w-0'>
          <Avatar
            size='small'
            color={highlight ? 'orange' : 'blue'}
            className='mr-3 shadow-sm flex-shrink-0'
          >
            {icon}
          </Avatar>
          <div className='min-w-0'>
            <div className='flex items-center gap-2'>
              <Text className='font-medium truncate'>{title}</Text>
              {pointsText && (
                <Tag color='orange' shape='circle' size='small'>
                  {pointsText} {t('积分')}
                </Tag>
              )}
            </div>
            <div className='text-xs text-gray-500 truncate'>{desc}</div>
          </div>
        </div>
        <Button
          theme={highlight ? 'solid' : 'light'}
          type='primary'
          size='small'
          className='flex-shrink-0 ml-2'
          onClick={onCta}
        >
          {ctaText}
        </Button>
      </div>
    );
  };

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      <div className='flex items-center mb-3'>
        <Avatar size='small' color='orange' className='mr-3 shadow-md'>
          <Coins size={16} />
        </Avatar>
        <div>
          <Title heading={5} className='m-0'>
            {t('赚取积分')}
          </Title>
          <Text className='text-xs text-gray-500'>
            {t('完成以下任务即可获得积分，用于抵扣消费')}
          </Text>
        </div>
      </div>
      <div className='space-y-2'>{tasks.map(renderTask)}</div>
    </Card>
  );
};

export default PointsTasksCard;
