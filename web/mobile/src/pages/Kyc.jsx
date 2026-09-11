import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Dialog,
  Form,
  Image,
  ImageViewer,
  Input,
  NavBar,
  SpinLoading,
} from 'antd-mobile';
import { AddOutline, CameraOutline } from 'antd-mobile-icons';

import { API } from '@classic/helpers/api';
// 压缩到「无 data: 前缀的纯 JPEG base64」，正是 /api/user/kyc 要的形态：
// 管理端取图时统一拼 data:image/jpeg 前缀（controller/kyc.go AdminGetKYCImages），
// 所以不能沿用本仓 utils/file.js 的 imageFileToDataUrl —— 它带前缀，且「够小就原样
// 返回」会把 PNG/HEIC 原样送上去，被贴成 jpeg 标签。
// 取 feedbackHelpers 这份，是因为它是这套参数（2400 长边 / JPEG 0.88，超 1.5MB 降一档）
// 唯一被导出的实现。注意桌面端 KYCSetting.jsx:42、EnterpriseSetting.jsx:55、
// BankTransferCard.jsx:48 各有一份逐字相同的模块内私有拷贝 —— 改压缩参数时它们不会
// 跟着变，别以为动这一个就全覆盖了。
import { compressImageToBase64 } from '@classic/components/feedback/feedbackHelpers';

import { showError, showSuccess } from '../shims/classic-utils';
import { KYC_USER_STATUS, formatTs } from '../utils/review';

// 与桌面端 KYCSetting.jsx 同一套校验：后端只管宽松的长度区间（2-32 / 6-30），
// 真正的「中文姓名 + 18 位身份证」约束在前端，两端保持一致以免一边能过一边不能。
const NAME_RE = /^[一-龥·]{2,25}$/;
const ID_RE = /^\d{17}[\dXx]$/;

// 单张身份证照片输入格：拍照（直接唤起后置摄像头）或从相册选。
// 两个 input 而不是一个：capture 一旦加上，安卓多数浏览器就只给相机、不给相册，
// 所以「拍照」「从相册选」各挂一个，用户点哪个走哪条。
const IdCardSlot = ({ label, value, onChange, disabled }) => {
  const pickRef = useRef(null);
  const captureRef = useRef(null);
  const [viewer, setViewer] = useState(false);

  const handlePick = async (e) => {
    const file = e.target.files?.[0];
    // 先清空再用：不清的话删掉重选同一张图不会触发 change
    e.target.value = '';
    if (!file) return;
    try {
      onChange(await compressImageToBase64(file));
    } catch (err) {
      showError('图片处理失败，请重试');
    }
  };

  const src = value ? `data:image/jpeg;base64,${value}` : '';

  return (
    <div style={{ flex: 1, minWidth: 0 }}>
      <div className='m-media-slot-label'>
        {label}
        <span style={{ color: 'var(--adm-color-danger)' }}>*</span>
      </div>
      {value ? (
        <div className='m-media-slot-row'>
          <Image
            src={src}
            width={72}
            height={72}
            fit='cover'
            // flex:0 0 auto 与 MediaBar 的同类槽位一致：m-media-slot-row 是 flex 容器，
            // 不锁的话窄屏上会被旁边的按钮列挤成非方框（两格并排各占半宽，比 MediaBar
            // 整行的场景更紧）
            style={{ borderRadius: 8, flex: '0 0 auto' }}
            onClick={() => setViewer(true)}
          />
          <div className='m-media-slot-col'>
            <div className='m-media-slot-actions'>
              <Button
                size='mini'
                fill='outline'
                disabled={disabled}
                onClick={() => pickRef.current?.click()}
              >
                重选
              </Button>
              <Button
                size='mini'
                fill='outline'
                color='danger'
                disabled={disabled}
                onClick={() => onChange('')}
              >
                删除
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <div className='m-media-slot-actions'>
          <Button
            size='small'
            fill='outline'
            disabled={disabled}
            onClick={() => captureRef.current?.click()}
          >
            <CameraOutline /> 拍照
          </Button>
          <Button
            size='small'
            fill='outline'
            disabled={disabled}
            onClick={() => pickRef.current?.click()}
          >
            <AddOutline /> 从相册选
          </Button>
        </div>
      )}
      <ImageViewer
        image={src}
        visible={viewer}
        onClose={() => setViewer(false)}
      />
      <input
        ref={pickRef}
        type='file'
        accept='image/*'
        hidden
        onChange={handlePick}
      />
      <input
        ref={captureRef}
        type='file'
        accept='image/*'
        capture='environment'
        hidden
        onChange={handlePick}
      />
    </div>
  );
};

const Kyc = () => {
  const navigate = useNavigate();
  const [kyc, setKyc] = useState(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [realName, setRealName] = useState('');
  const [idNumber, setIdNumber] = useState('');
  const [front, setFront] = useState('');
  const [back, setBack] = useState('');
  const [loadFailed, setLoadFailed] = useState(false);

  const loadKyc = useCallback(async () => {
    setLoading(true);
    setLoadFailed(false);
    try {
      const res = await API.get('/api/user/kyc');
      const { success, message, data } = res.data;
      if (success) {
        setKyc(data);
        // 未认证的人进来就是为了填表，少一次点击
        if ((data?.status ?? 0) === 0) setEditing(true);
      } else {
        setLoadFailed(true);
        showError(message);
      }
    } catch (e) {
      setLoadFailed(true);
      showError(e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadKyc();
  }, [loadKyc]);

  const status = kyc?.status ?? 0;
  const view = KYC_USER_STATUS[status] || KYC_USER_STATUS[0];

  const openForm = () => {
    // 姓名可带出（驳回多半是照片问题），证件号一律重填，避免从掩码回填出错号码
    setRealName(kyc?.real_name || '');
    setIdNumber('');
    setFront('');
    setBack('');
    setEditing(true);
  };

  const handleSubmit = async () => {
    const name = realName.trim();
    const id = idNumber.trim().toUpperCase();
    if (!NAME_RE.test(name)) {
      showError('请输入 2-25 位中文姓名');
      return;
    }
    if (!ID_RE.test(id)) {
      showError('请输入 18 位有效身份证号码（末位可为 X）');
      return;
    }
    if (!front || !back) {
      showError('请上传身份证正反面照片');
      return;
    }
    setSubmitting(true);
    try {
      // 首次提交走 POST，被驳回后重新提交走 PUT —— 后端按当前状态分流，用错那个
      // 会直接被挡（controller/kyc.go：0 状态 PUT 报「尚未提交认证」，
      // 非 0 状态 POST 报「已有待审核记录」）
      const method = status === 0 ? 'post' : 'put';
      const res = await API[method]('/api/user/kyc', {
        real_name: name,
        id_type: 'id_card',
        id_number: id,
        id_card_front: front,
        id_card_back: back,
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess('提交成功，等待管理员审核');
        setEditing(false);
        setIdNumber('');
        setFront('');
        setBack('');
        await loadKyc();
      } else {
        showError(message);
      }
    } catch (e) {
      showError(e);
    } finally {
      setSubmitting(false);
    }
  };

  const handleRevoke = async () => {
    const confirmed = await Dialog.confirm({
      content: '确认撤回实名认证申请？撤回后可重新提交。',
    });
    if (!confirmed) return;
    try {
      const res = await API.delete('/api/user/kyc');
      const { success, message } = res.data;
      if (success) {
        showSuccess('已撤回');
        await loadKyc();
      } else {
        showError(message);
      }
    } catch (e) {
      showError(e);
    }
  };

  return (
    <div>
      <NavBar onBack={() => navigate(-1)}>实名认证</NavBar>

      {loading ? (
        <div style={{ textAlign: 'center', padding: '60px 0' }}>
          <SpinLoading style={{ '--size': '32px', margin: '0 auto' }} />
        </div>
      ) : (
        <div style={{ padding: 12 }}>
          <div className='m-card'>
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <div style={{ fontWeight: 600 }}>认证状态</div>
              {!loadFailed && (
                <span className={`m-badge ${view.badge}`}>{view.text}</span>
              )}
            </div>

            {/*
              取不到状态时只给重试，不渲染任何由 status 推出来的东西。两条理由：
                kyc 为 null 时 status 兜底成 0，照着渲染就是把「状态未知」说成「未认证」，
                  而 0 没有按钮分支，用户会卡在一张无按钮的卡片上、只能重进页面；
                提交/撤回成功后 reload 失败也落到这里（editing 已关、kyc 还是旧的），
                  那份状态已经过期，照着渲染会摆出一个点了必报错的撤回按钮。
              桌面端 KYCSetting.jsx:334 是给 status 0 配「立即认证」按钮来绕开死胡同，
              这里不照搬：状态未知时引导填表，万一用户其实已认证，要等拍完两张照片提交了
              才被后端驳回。
            */}
            {loadFailed ? (
              <>
                <div style={{ fontSize: 13, color: '#b91c1c', marginTop: 10 }}>
                  认证状态加载失败
                </div>
                <Button
                  block
                  size='small'
                  fill='outline'
                  style={{ marginTop: 12 }}
                  onClick={loadKyc}
                >
                  重新加载
                </Button>
              </>
            ) : (
              <>
                {status !== 0 && (
                  <div
                    style={{ fontSize: 13, color: '#6b7280', marginTop: 10 }}
                  >
                    <div>姓名：{kyc?.real_name}</div>
                    <div>证件号：{kyc?.id_number_masked}</div>
                    {kyc?.submitted_at && (
                      <div>提交时间：{formatTs(kyc.submitted_at)}</div>
                    )}
                    {status === 2 && kyc?.verified_at && (
                      <div>认证时间：{formatTs(kyc.verified_at)}</div>
                    )}
                    {/* 提交次数上限由后端 KYCMaxSubmitCount 配置、未下发到前端，
                    所以只报已用次数，不替后端算剩余；超限时提交会被后端拦下并给出提示 */}
                    {kyc?.submit_count > 1 && (
                      <div>已提交 {kyc.submit_count} 次</div>
                    )}
                  </div>
                )}

                {status === 3 && kyc?.reject_reason && (
                  <div style={{ fontSize: 13, color: '#b91c1c', marginTop: 8 }}>
                    驳回原因：{kyc.reject_reason}
                  </div>
                )}

                {status === 1 && (
                  <>
                    <div
                      style={{ fontSize: 13, color: '#6b7280', marginTop: 10 }}
                    >
                      已提交，等待管理员审核
                    </div>
                    <Button
                      block
                      size='small'
                      color='danger'
                      fill='outline'
                      style={{ marginTop: 12 }}
                      onClick={handleRevoke}
                    >
                      撤回申请
                    </Button>
                  </>
                )}

                {status === 3 && !editing && (
                  <Button
                    block
                    size='small'
                    color='primary'
                    style={{ marginTop: 12 }}
                    onClick={openForm}
                  >
                    重新提交
                  </Button>
                )}
              </>
            )}
          </div>

          {editing && (
            <Form
              layout='vertical'
              style={{ marginTop: 12 }}
              footer={
                <Button
                  block
                  color='primary'
                  loading={submitting}
                  onClick={handleSubmit}
                >
                  提交认证
                </Button>
              }
            >
              <Form.Header>
                {status === 0 ? '填写认证信息' : '重新填写认证信息'}
              </Form.Header>
              <Form.Item label='真实姓名'>
                <Input
                  placeholder='请输入与身份证一致的姓名'
                  value={realName}
                  onChange={setRealName}
                  disabled={submitting}
                />
              </Form.Item>
              <Form.Item label='身份证号码'>
                <Input
                  placeholder='请输入 18 位身份证号码'
                  value={idNumber}
                  onChange={setIdNumber}
                  disabled={submitting}
                />
              </Form.Item>
              <Form.Item label='身份证照片'>
                <div style={{ display: 'flex', gap: 12, marginTop: 4 }}>
                  <IdCardSlot
                    label='正面（人像面）'
                    value={front}
                    onChange={setFront}
                    disabled={submitting}
                  />
                  <IdCardSlot
                    label='背面（国徽面）'
                    value={back}
                    onChange={setBack}
                    disabled={submitting}
                  />
                </div>
                <div
                  style={{
                    fontSize: 11.5,
                    color: '#9ca3af',
                    marginTop: 8,
                    lineHeight: 1.5,
                  }}
                >
                  可直接拍照或从相册选图，上传前自动压缩。
                  <br />
                  照片仅用于身份核验，加密存储。
                </div>
              </Form.Item>
            </Form>
          )}
        </div>
      )}
    </div>
  );
};

export default Kyc;
