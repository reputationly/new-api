import React, { useContext, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { Empty, Grid, NavBar } from 'antd-mobile';
import {
  MessageOutline,
  MovieOutline,
  PictureOutline,
  PlayOutline,
  SoundOutline,
} from 'antd-mobile-icons';

import { StatusContext } from '@classic/context/Status';
import { mergeAdminConfig } from '@classic/hooks/common/useSidebar';

// moduleKey = 后台「侧边栏模块」里的模块键，决定该入口是否展示。
// 注意「对话」对应的是 playground（文本模型体验区），不是后台那个 chat 键
// —— 后者指「聊天会话管理」，是另一个功能，接错会让对话入口跟着它一起消失。
const areas = [
  {
    key: 'chat',
    moduleKey: 'playground',
    title: '对话',
    desc: '流式聊天，支持思考过程',
    icon: <MessageOutline />,
    bg: 'linear-gradient(135deg,#4f46e5,#7970ee)',
    path: '/chat',
  },
  {
    key: 'video',
    moduleKey: 'video',
    title: '视频生成',
    desc: '文生 · 图生 · 数字人',
    icon: <MovieOutline />,
    bg: 'linear-gradient(135deg,#9d3b8f,#c05aa8)',
    path: '/video',
  },
  {
    key: 'music',
    moduleKey: 'music',
    title: '音乐音效',
    desc: '音乐 · 音效 · 歌声合成',
    icon: <PlayOutline />,
    bg: 'linear-gradient(135deg,#b07a2f,#d2a253)',
    path: '/music',
  },
  {
    key: 'audio',
    moduleKey: 'audio',
    title: '语音合成',
    desc: '情感合成 · 声音设计',
    icon: <SoundOutline />,
    bg: 'linear-gradient(135deg,#0f766e,#2ba79b)',
    path: '/audio',
  },
  {
    key: 'image',
    moduleKey: 'image',
    title: '图像生成',
    desc: '文生图 · 图生图',
    icon: <PictureOutline />,
    bg: 'linear-gradient(135deg,#1d5f9e,#3f83c4)',
    path: '/image',
  },
];

const Experience = () => {
  const navigate = useNavigate();
  const [statusState] = useContext(StatusContext);

  // 入口显隐跟随后台「侧边栏模块 → 体验区域」配置，与桌面端同一真相源：
  // 桌面端已关掉的分类，手机端不该还留个点进去只有空态的入口。
  const visibleAreas = useMemo(() => {
    const raw = statusState?.status?.SidebarModulesAdmin;
    let config;
    try {
      config = mergeAdminConfig(raw ? JSON.parse(raw) : null);
    } catch (e) {
      config = mergeAdminConfig(null);
    }
    const section = config.chat || {};
    if (section.enabled === false) return [];
    return areas.filter((a) => section[a.moduleKey] !== false);
  }, [statusState?.status?.SidebarModulesAdmin]);

  return (
    <div>
      <NavBar back={null}>体验区</NavBar>
      <div style={{ padding: 12 }}>
        {visibleAreas.length === 0 ? (
          <Empty style={{ padding: 32 }} description='当前体验区暂未开放' />
        ) : (
          <>
            <Grid columns={2} gap={12}>
              {visibleAreas.map((a) => (
                <Grid.Item key={a.key}>
                  <div className='m-tile' onClick={() => navigate(a.path)}>
                    <div className='tile-icon' style={{ background: a.bg }}>
                      {a.icon}
                    </div>
                    <div className='tile-title'>{a.title}</div>
                    <div className='tile-desc'>{a.desc}</div>
                  </div>
                </Grid.Item>
              ))}
            </Grid>
            <p
              style={{
                textAlign: 'center',
                fontSize: 12,
                color: '#c0c4cc',
                marginTop: 20,
              }}
            >
              更多高级参数（随机种子、负向提示词等）请前往电脑端
            </p>
          </>
        )}
      </div>
    </div>
  );
};

export default Experience;
