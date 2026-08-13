import React, { useContext, useEffect, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, Form, Input, Toast } from 'antd-mobile';
import Turnstile from 'react-turnstile';

import { StatusContext } from '@classic/context/Status';
import { UserContext } from '@classic/context/User';
import { API, updateAPI } from '@classic/helpers/api';
import {
  LOGIN_REDIRECT_PARAM,
  safeRedirectTarget,
} from '@classic/helpers/authRedirect';
import { setUserData } from '@classic/helpers/data';

import {
  showError,
  showSuccess,
  getSystemName,
  getLogo,
} from '../shims/classic-utils';
import { useAgreementGate } from '../components/AgreementGate';
import { isWeChatBrowser } from '../utils/share';

// 微信内置浏览器接不上系统密码管理器（iOS 的 WKWebView 与安卓的 X5/XWeb 都是），
// 用户每次都得手打账号密码。这里退而求其次记住用户名（密码绝不落盘），至少省一半输入。
const REMEMBERED_USERNAME_KEY = 'm_login_username';

const Login = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const [statusState] = useContext(StatusContext);
  const [, userDispatch] = useContext(UserContext);

  // 惰性初始值而非 useEffect 回填：浏览器的自动填充永远晚于挂载发生，只在挂载写一次
  // 就不会把它填的那一组覆盖掉。否则用户从密码管理器选了账号 A（同时填好 A 的密码），
  // 我们再把用户名改回记住的 B，就成了「用户名 B + A 的密码」，登录失败还查不出原因。
  // 只在微信里预填：普通浏览器原生自动填充本来就好用，字段非空反而会让账号选择条不弹。
  const [username, setUsername] = useState(() =>
    isWeChatBrowser()
      ? localStorage.getItem(REMEMBERED_USERNAME_KEY) || ''
      : '',
  );
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [turnstileToken, setTurnstileToken] = useState('');
  // 2FA 分支：普通登录返回 require_2fa 后切换到验证码输入
  const [require2FA, setRequire2FA] = useState(false);
  const [twoFACode, setTwoFACode] = useState('');
  const { agreementNode, ensureAgreed } = useAgreementGate();

  const status = statusState?.status || {};
  const turnstileEnabled = !!status.turnstile_check;
  const turnstileSiteKey = status.turnstile_site_key || '';

  // 回跳目标的两个来源：
  //   - location.state.from：AuthRoute 在 SPA 内跳登录页时带的，信息最全，优先；
  //   - ?redirect=：会话过期被 handleUnauthorized 整页跳回来的，state 在整页跳转下
  //     活不下来，只能走 query。必须过 safeRedirectTarget 挡开放重定向。
  const from =
    location.state?.from?.pathname ||
    safeRedirectTarget(
      new URLSearchParams(location.search).get(LOGIN_REDIRECT_PARAM),
    ) ||
    '/';

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get('expired')) {
      Toast.show({ content: '登录已过期，请重新登录' });
    }
  }, []);

  const finishLogin = (data) => {
    // 记住用户实际输入的那个标识（可能是用户名也可能是邮箱），而不是后端回的
    // 规范化用户名 —— 下次他还是会照原样再打一遍。2FA 分支也经过这里，一处即可。
    // 必须 try 住：无痕模式/存储配额满时 setItem 会抛，裸写会把 finishLogin 整个中断，
    // 变成"登录明明成功却停在登录页且没有任何提示"。记用户名是锦上添花，不能挡住登录。
    try {
      if (username.trim()) {
        localStorage.setItem(REMEMBERED_USERNAME_KEY, username.trim());
      }
    } catch (e) {
      // 记不住就算了，不影响登录
    }
    userDispatch({ type: 'login', payload: data });
    setUserData(data);
    updateAPI();
    showSuccess('登录成功！');
    navigate(from, { replace: true });
  };

  const handleSubmit = async () => {
    if (!ensureAgreed()) return;
    if (!username || !password) {
      Toast.show({ content: '请输入用户名和密码' });
      return;
    }
    if (turnstileEnabled && !turnstileToken) {
      Toast.show({ content: '请稍候，正在进行安全验证……' });
      return;
    }
    setSubmitting(true);
    try {
      const res = await API.post(
        `/api/user/login?turnstile=${turnstileToken}`,
        { username, password },
      );
      const { success, message, data } = res.data;
      if (success) {
        if (data && data.require_2fa) {
          setRequire2FA(true);
          Toast.show({ content: '请输入两步验证码' });
        } else {
          finishLogin(data);
        }
      } else {
        showError(message);
      }
    } catch (e) {
      // 401/429 等由 showError 统一处理
      showError(e);
    } finally {
      setSubmitting(false);
    }
  };

  const handle2FASubmit = async () => {
    if (!twoFACode) {
      Toast.show({ content: '请输入验证码' });
      return;
    }
    setSubmitting(true);
    try {
      const res = await API.post('/api/user/login/2fa', { code: twoFACode });
      const { success, message, data } = res.data;
      if (success) {
        finishLogin(data);
      } else {
        showError(message);
      }
    } catch (e) {
      showError(e);
    } finally {
      setSubmitting(false);
    }
  };

  // 站点名/logo 来自 /api/status 写入的 localStorage，跟随运营配置
  const systemName = status.system_name || getSystemName();
  const logo = status.logo || getLogo();

  return (
    <div className='m-login-bg'>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          paddingTop: 72,
          paddingBottom: 32,
        }}
      >
        <img className='m-login-logo' src={logo} alt='logo' />
        <h2 style={{ margin: '18px 0 4px', fontSize: 22 }}>{systemName}</h2>
        <div style={{ fontSize: 13, color: '#9ca3af' }}>
          {require2FA ? '两步验证' : '登录后开始体验'}
        </div>
      </div>
      <div className='m-login-card'>
        {!require2FA ? (
          <Form
            layout='vertical'
            onFinish={handleSubmit}
            footer={
              <Button block color='primary' loading={submitting} type='submit'>
                登录
              </Button>
            }
          >
            <Form.Item label='用户名 / 邮箱'>
              <Input
                placeholder='请输入用户名或邮箱'
                value={username}
                onChange={setUsername}
                name='username'
                autoComplete='username'
              />
            </Form.Item>
            <Form.Item label='密码'>
              <Input
                placeholder='请输入密码'
                type='password'
                value={password}
                onChange={setPassword}
                name='password'
                autoComplete='current-password'
                onEnterPress={handleSubmit}
              />
            </Form.Item>
          </Form>
        ) : (
          <Form
            layout='vertical'
            footer={
              <Button
                block
                color='primary'
                loading={submitting}
                onClick={handle2FASubmit}
              >
                验证并登录
              </Button>
            }
          >
            <Form.Item label='两步验证码'>
              <Input
                placeholder='验证器 6 位数字或备用码'
                value={twoFACode}
                onChange={setTwoFACode}
                onEnterPress={handle2FASubmit}
              />
            </Form.Item>
          </Form>
        )}
      </div>
      {agreementNode}
      {turnstileEnabled && turnstileSiteKey && (
        <div
          style={{ display: 'flex', justifyContent: 'center', marginTop: 16 }}
        >
          <Turnstile
            sitekey={turnstileSiteKey}
            onVerify={(token) => setTurnstileToken(token)}
          />
        </div>
      )}
      <p
        style={{
          textAlign: 'center',
          color: 'var(--adm-color-weak)',
          fontSize: 13,
          marginTop: 24,
        }}
      >
        没有账户？
        {/* 透传 query：从 /m/login?aff=xxx 点过来时别把邀请码弄丢 */}
        <Link to={{ pathname: '/register', search: window.location.search }}>
          立即注册
        </Link>{' '}
        · 找回密码请前往电脑端
        <br />
        {/* 移动端没有 OAuth；desktop=1 让后端中间件放行，否则会被弹回 /m/login
            （见 router/mobile-router.go）。 */}
        使用 GitHub 等其他方式登录请
        <a href='/login?desktop=1'>前往电脑端</a>
      </p>
    </div>
  );
};

export default Login;
