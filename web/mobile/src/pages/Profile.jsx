import React, {
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Dialog, List, NavBar, Popup } from 'antd-mobile';
import { QRCodeCanvas } from 'qrcode.react';

import { UserContext } from '@classic/context/User';
import { API, updateAPI } from '@classic/helpers/api';

import { copy, isAdmin, showError, showSuccess } from '../shims/classic-utils';
import { pointsEnabled, renderPoints, renderQuota } from '../utils/quota';

const Profile = () => {
  const navigate = useNavigate();
  const [userState, userDispatch] = useContext(UserContext);
  const [self, setSelf] = useState(null);
  const [checkinEnabled, setCheckinEnabled] = useState(false);
  const [checkedInToday, setCheckedInToday] = useState(false);
  const [ticketUnread, setTicketUnread] = useState(0);
  // 管理员待办角标：{kyc, enterprise, bank_transfer, invoice} + 工单未读
  const [pendingCounts, setPendingCounts] = useState(null);
  const [adminTicketUnread, setAdminTicketUnread] = useState(0);
  const [affLink, setAffLink] = useState('');
  const admin = isAdmin();

  const loadSelf = useCallback(async () => {
    try {
      const res = await API.get('/api/user/self');
      if (res.data.success) setSelf(res.data.data);
    } catch (e) {
      showError(e);
    }
  }, []);

  const loadCheckin = useCallback(async () => {
    try {
      const res = await API.get('/api/user/checkin', {
        skipErrorHandler: true,
      });
      if (res.data.success) {
        setCheckinEnabled(!!res.data.data?.enabled);
        setCheckedInToday(!!res.data.data?.stats?.checked_in_today);
      }
    } catch (e) {
      // 签到未启用时静默
    }
  }, []);

  // 邀请链接指向桌面路径 /register?aff=xxx 而不是 /m/register：router/mobile-router.go
  // 会把手机 UA 的 /register 带 query 跳到 /m/register，桌面 UA 则留在桌面版 ——
  // 一条链接两端通吃，用户不用管接链接的人拿什么设备打开。
  // 挂载时就预取，好让点击时能同步复制（原因见 handleCopyAffLink）。
  // silent=true 用于预取：子账户会被 SubAccountForbidden 拦下，不该一进页面就弹错。
  const fetchAffLink = useCallback(async ({ silent } = { silent: true }) => {
    try {
      const res = await API.get('/api/user/aff', { skipErrorHandler: silent });
      const { success, message, data } = res.data;
      if (!success || !data) {
        if (!silent) showError(message || '获取邀请码失败');
        return '';
      }
      const link = `${window.location.origin}/register?aff=${data}`;
      setAffLink(link);
      return link;
    } catch (e) {
      if (!silent) showError(e);
      return '';
    }
  }, []);

  const loadBadges = useCallback(async () => {
    try {
      const res = await API.get('/api/user/feedback/unread', {
        skipErrorHandler: true,
      });
      if (res.data.success) {
        setTicketUnread(res.data.data?.unread || 0);
      }
    } catch (e) {
      // 静默
    }
    if (isAdmin()) {
      try {
        const [countsRes, unreadRes] = await Promise.all([
          API.get('/api/user/review/pending_counts', {
            skipErrorHandler: true,
          }),
          API.get('/api/user/feedback/admin/unread', {
            skipErrorHandler: true,
          }),
        ]);
        if (countsRes.data.success) setPendingCounts(countsRes.data.data);
        if (unreadRes.data.success) {
          setAdminTicketUnread(
            unreadRes.data.data?.unread ?? unreadRes.data.data ?? 0,
          );
        }
      } catch (e) {
        // 静默
      }
    }
  }, []);

  useEffect(() => {
    loadSelf();
    loadCheckin();
    loadBadges();
    fetchAffLink();
  }, [loadSelf, loadCheckin, loadBadges, fetchAffLink]);

  const badge = (n) =>
    n > 0 ? <span className='m-badge danger'>{n}</span> : null;

  const handleCheckin = async () => {
    try {
      const res = await API.post('/api/user/checkin');
      const { success, message } = res.data;
      if (success) {
        showSuccess('签到成功！');
        setCheckedInToday(true);
        loadSelf();
      } else {
        showError(message);
      }
    } catch (e) {
      showError(e);
    }
  };

  const handleCopyAffLink = async () => {
    // 走到这里 affLink 通常已由挂载时的 loadAffLink 预取好，直接同步复制。
    // 这一点是要害：navigator.clipboard.writeText 要求 transient user activation，
    // 而点击后先 await 一个网络请求会把这个激活窗口耗掉，弱网下必然降级到
    // execCommand 兜底（新版 WebView 里也可能静默失败）。预取失败时才现取，
    // 那条路仍有手势过期风险，但兜底弹层能保证用户至少拿得到链接。
    let link = affLink;
    if (!link) {
      link = await fetchAffLink({ silent: false });
      if (!link) return;
    }
    if (await copy(link)) {
      showSuccess('专属邀请链接已复制到剪切板');
    } else {
      // 剪贴板被浏览器拦下时把链接摆出来让用户长按复制，别让人白点一下拿不到东西
      Dialog.alert({ title: '专属邀请链接', content: link });
    }
  };

  // 二维码要以 <img> 呈现，长按才会弹出系统的「保存图片」——canvas 和 svg 长按都没有
  // 这个菜单。所以先用离屏 canvas 画出来，再 toDataURL 成一张真正的图片。
  const qrCanvasRef = useRef(null);
  const [qrVisible, setQrVisible] = useState(false);
  const [qrImage, setQrImage] = useState('');

  const handleShowAffQr = async () => {
    let link = affLink;
    if (!link) {
      link = await fetchAffLink({ silent: false });
      if (!link) return;
    }
    setQrVisible(true);
  };

  // 拿到链接就转图，不等弹层打开。
  //
  // 离屏 canvas 常驻在页面根部而不是放进 Popup：放进去的话这段 effect 就得依赖
  // Popup 何时挂载子树才能 querySelector 到 canvas——那是 antd-mobile 的内部实现
  // 细节（当前靠 useLayoutEffect 同步挂载才成立），它哪天改成动画结束后再挂，
  // 这里就拿到 null，表现是「二维码生成中…」永久卡住，且只有真机才看得见。
  // 常驻之后没有任何时序依赖，代价只是一张 480px 的隐藏画布。
  useEffect(() => {
    if (!affLink) return;
    const canvas = qrCanvasRef.current?.querySelector('canvas');
    if (!canvas) return;
    try {
      setQrImage(canvas.toDataURL('image/png'));
    } catch (e) {
      // toDataURL 在极少数隐私模式下会抛，退化成不展示图片而不是白屏
      setQrImage('');
    }
  }, [affLink]);

  const handleLogout = async () => {
    const confirmed = await Dialog.confirm({ content: '确定退出登录吗？' });
    if (!confirmed) return;
    try {
      await API.get('/api/user/logout');
    } catch (e) {
      // 即使接口失败也清理本地状态
    }
    localStorage.removeItem('user');
    userDispatch({ type: 'logout' });
    updateAPI();
    navigate('/login', { replace: true });
  };

  const user = self || userState?.user || {};

  return (
    <div>
      <NavBar back={null}>我的</NavBar>
      <div style={{ padding: 12 }}>
        <div
          className='m-hero'
          style={{ display: 'flex', alignItems: 'center', gap: 14 }}
        >
          <div
            style={{
              width: 56,
              height: 56,
              borderRadius: '50%',
              background: 'rgba(255,255,255,0.25)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 24,
              fontWeight: 700,
              flexShrink: 0,
            }}
          >
            {(user.display_name || user.username || '?')[0]?.toUpperCase()}
          </div>
          <div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>
              {user.display_name || user.username}
            </div>
            {self && (
              <div style={{ fontSize: 13, opacity: 0.85, marginTop: 2 }}>
                剩余额度 {renderQuota(self.quota)} · 已用{' '}
                {renderQuota(self.used_quota)}
                {pointsEnabled() &&
                  ` · 积分 ${renderPoints(self.points_balance)}（已用 ${renderPoints(self.points_used)}）`}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className='m-section-title'>账户</div>
      <List
        className='m-list-card'
        style={{ '--border-top': 'none', '--border-bottom': 'none' }}
      >
        {checkinEnabled && (
          <List.Item
            extra={checkedInToday ? '今日已签到' : ''}
            onClick={checkedInToday ? undefined : handleCheckin}
          >
            每日签到
          </List.Item>
        )}
        <List.Item onClick={() => navigate('/logs')}>使用日志</List.Item>
        <List.Item
          description='点击复制，好友通过该链接注册即计入你的邀请'
          onClick={handleCopyAffLink}
        >
          我的邀请链接
        </List.Item>
        <List.Item
          description='点击查看，可保存或截图分享给好友'
          onClick={handleShowAffQr}
        >
          我的邀请二维码
        </List.Item>
        <List.Item
          extra={badge(ticketUnread)}
          onClick={() => navigate('/tickets')}
        >
          我的工单
        </List.Item>
        <List.Item onClick={() => navigate('/setting')}>账户设置</List.Item>
      </List>

      {admin && (
        <>
          <div className='m-section-title'>管理</div>
          <List
            className='m-list-card'
            style={{ '--border-top': 'none', '--border-bottom': 'none' }}
          >
            <List.Item
              extra={badge(adminTicketUnread)}
              onClick={() => navigate('/admin/tickets')}
            >
              工单管理
            </List.Item>
            <List.Item
              extra={badge(pendingCounts?.kyc)}
              onClick={() => navigate('/admin/kyc')}
            >
              实名认证审批
            </List.Item>
            <List.Item
              extra={badge(pendingCounts?.enterprise)}
              onClick={() => navigate('/admin/enterprise')}
            >
              企业认证审批
            </List.Item>
            <List.Item
              extra={badge(pendingCounts?.bank_transfer)}
              onClick={() => navigate('/admin/transfers')}
            >
              企业转账审核
            </List.Item>
            <List.Item
              extra={badge(pendingCounts?.invoice)}
              onClick={() => navigate('/admin/invoices')}
            >
              企业开票审核
            </List.Item>
          </List>
        </>
      )}

      <div style={{ padding: 16 }}>
        <Button block color='danger' fill='outline' onClick={handleLogout}>
          退出登录
        </Button>
      </div>

      {/*
        离屏画布：常驻页面、只为拿 dataURL，不出现在视觉上。affLink 由挂载时预取，
        所以点开弹层时图片通常已经就绪，不会看到「生成中」。
      */}
      <div ref={qrCanvasRef} style={{ display: 'none' }} aria-hidden>
        {affLink ? <QRCodeCanvas value={affLink} size={480} /> : null}
      </div>

      <Popup
        visible={qrVisible}
        onMaskClick={() => setQrVisible(false)}
        onClose={() => setQrVisible(false)}
        bodyStyle={{ borderTopLeftRadius: 12, borderTopRightRadius: 12 }}
      >
        <div
          style={{
            padding: 24,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 12,
          }}
        >
          <div style={{ fontSize: 16, fontWeight: 600 }}>我的邀请二维码</div>

          {qrImage ? (
            // 白底留边有两个作用：深色模式下不至于和背景糊在一起导致扫不出，
            // 保存到相册后也仍是一张能直接扫的图。
            <div style={{ background: '#fff', padding: 12, borderRadius: 8 }}>
              <img
                src={qrImage}
                alt='邀请二维码'
                style={{ width: 220, height: 220, display: 'block' }}
              />
            </div>
          ) : (
            <div style={{ color: 'var(--adm-color-weak)', fontSize: 13 }}>
              二维码生成中…
            </div>
          )}

          {/*
            不写死「长按一定能保存」：微信内置浏览器对 base64 图片的长按菜单在部分
            版本（尤其 iOS）不弹出，写成承诺会让人以为是页面坏了。截图是所有浏览器
            都有的兜底，且截出来的二维码照样能扫、能被微信长按识别。
          */}
          <div
            style={{
              color: 'var(--adm-color-weak)',
              fontSize: 13,
              textAlign: 'center',
            }}
          >
            长按二维码可保存到相册（部分浏览器需截图保存）
            <br />
            好友扫码注册即计入你的邀请
          </div>
          <div
            style={{
              color: 'var(--adm-color-weak)',
              fontSize: 12,
              wordBreak: 'break-all',
              textAlign: 'center',
            }}
          >
            {affLink}
          </div>
          <Button block onClick={() => setQrVisible(false)}>
            关闭
          </Button>
        </div>
      </Popup>
    </div>
  );
};

export default Profile;
