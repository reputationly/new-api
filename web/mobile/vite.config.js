import react from '@vitejs/plugin-react';
import path from 'path';
import { defineConfig, transformWithEsbuild } from 'vite';
import { VitePWA } from 'vite-plugin-pwa';

const classicSrc = path.resolve(__dirname, '../classic/src');
const shims = path.resolve(__dirname, 'src/shims');

// classic 的 helpers/utils.jsx 及 helpers/index.js barrel 会传染 Semi/桌面依赖，
// 按"解析后的绝对路径"整模块替换为移动端 shim（antd-mobile 实现 + 拷贝的纯函数）。
const SHIM_MAP = {
  [path.join(classicSrc, 'helpers/index.js')]: path.join(
    shims,
    'classic-helpers.js',
  ),
  [path.join(classicSrc, 'helpers/utils.jsx')]: path.join(
    shims,
    'classic-utils.jsx',
  ),
};

// classic 源码里的裸包导入（sse.js/localforage 等）按 Node 规则从 classic 目录向上
// 找 node_modules——本地因 classic 装过依赖能碰巧解析，Docker 的 builder-mobile
// 阶段只拷源码不装 classic 依赖，会直接构建失败。统一改从 mobile 根解析，
// 本地与 CI 行为归一（mobile 的 package.json 必须包含复用链路的全部三方包）。
function classicBareImports() {
  return {
    name: 'classic-bare-imports',
    enforce: 'pre',
    async resolveId(source, importer, options) {
      if (!importer || !importer.includes(`${path.sep}classic${path.sep}src${path.sep}`)) {
        return null;
      }
      if (
        source.startsWith('.') ||
        source.startsWith('/') ||
        source.startsWith('@classic') ||
        source.startsWith('@douyinfe') // 交给 semi-ui shim 的 alias 处理
      ) {
        return null;
      }
      return this.resolve(source, path.resolve(__dirname, 'index.html'), {
        skipSelf: true,
        ...options,
      });
    },
  };
}

function classicShims() {
  return {
    name: 'classic-shims',
    enforce: 'pre',
    async resolveId(source, importer, options) {
      if (!importer) return null;
      const resolved = await this.resolve(source, importer, {
        skipSelf: true,
        ...options,
      });
      if (resolved && SHIM_MAP[resolved.id]) {
        return SHIM_MAP[resolved.id];
      }
      return null;
    },
  };
}

export default defineConfig({
  base: '/m/',
  resolve: {
    alias: [
      { find: '@', replacement: path.resolve(__dirname, 'src') },
      { find: '@classic', replacement: classicSrc },
      // 精确匹配：复用的 classic hooks 只允许用到 shim 导出的 Toast/Modal，
      // 未来若 classic 引入更多 Semi 组件，移动端构建会显式失败而非静默打包 Semi
      {
        find: /^@douyinfe\/semi-ui$/,
        replacement: path.join(shims, 'semi-ui.jsx'),
      },
    ],
    // 跨树 import ../classic/src 时防止解析到 classic/node_modules 的第二份依赖
    dedupe: [
      'react',
      'react-dom',
      'react-i18next',
      'i18next',
      'axios',
      'localforage',
    ],
  },
  plugins: [
    classicBareImports(),
    classicShims(),
    {
      // 与 classic 相同：classic 的 .js 文件可能含 JSX
      name: 'treat-js-files-as-jsx',
      async transform(code, id) {
        if (!/src\/.*\.js$/.test(id)) {
          return null;
        }
        return transformWithEsbuild(code, id, {
          loader: 'jsx',
          jsx: 'automatic',
        });
      },
    },
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      // 不生成静态 manifest：PWA 清单由服务端按运营配置实时下发
      // （router/mobile-router.go 拦 /m/manifest.webmanifest → router/brand.go
      // MobileWebManifest()）。插件生成的那份是构建期快照，写死 New API + 内置图标，
      // 且会被它自己塞进 SW 预缓存（globIgnores 拦不住，是插件另一条注入路径），
      // 之后「添加到主屏幕」永远拿到错的名字和图标。
      // index.html 里的 <link rel="manifest"> 因此改为手写，指向同一个动态路由。
      manifest: false,
      workbox: {
        // 必须显式置 undefined，不能靠"不写"——vite-plugin-pwa 的 defaultWorkbox 里
        // navigateFallback 默认就是 'index.html'（见 dist/index.cjs 的 defaultWorkbox），
        // 省略等于沿用默认。而 index.html 已被排除出预缓存，workbox 的
        // createHandlerBoundToURL 对未预缓存的 URL 会在 SW 脚本顶层直接抛
        // non-precached-url —— SW 整个装不上：runtimeCaching 失效、Chrome 因为没有
        // SW 不再提供「添加到主屏幕」，且老用户机器上的旧 SW 会继续接管、永远更新不掉。
        //
        // 关掉导航兜底后，导航一律走网络，交给服务端 handler 的 SPA fallback
        // （router/mobile-router.go 里 !canvasFileExists → BrandIndexHTML），
        // 这样运营配置的站点名与 logo 才能真正生效。
        // 代价是断网打不开应用；对一个必须联网才有意义的 AI 网关来说，这笔交易划算。
        navigateFallback: undefined,
        //
        // 预缓存排除三类：
        //   index.html / manifest.webmanifest —— 服务端实时做品牌替换（见 router/brand.go），
        //     一旦被 SW 接管就永远返回构建期的默认品牌；
        //   JS —— 占 dist 绝大部分体积，SW 安装与首屏渲染并发，等于自己跟自己抢带宽，
        //     改走下面的运行时缓存，用到哪个缓存哪个。
        globPatterns: ['**/*.{css,ico,png,svg}'],
        globIgnores: ['index.html', 'manifest.webmanifest'],
        maximumFileSizeToCacheInBytes: 4 * 1024 * 1024,
        runtimeCaching: [
          {
            // 文件名带 build hash 且服务端已下发 immutable，内容永不变 —— CacheFirst
            // 直接命中，不必再发校验请求。新版本会是新文件名，自然走新条目。
            urlPattern: /\/m\/assets\/.*\.js$/,
            handler: 'CacheFirst',
            options: {
              cacheName: 'mobile-js',
              expiration: { maxEntries: 60, maxAgeSeconds: 60 * 60 * 24 * 30 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
        ],
      },
    }),
  ],
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          'react-core': ['react', 'react-dom', 'react-router-dom'],
          'antd-mobile': ['antd-mobile', 'antd-mobile-icons'],
          i18n: ['i18next', 'react-i18next'],
          tools: ['axios', 'localforage', 'dayjs'],
        },
      },
    },
  },
  optimizeDeps: {
    esbuildOptions: {
      loader: {
        '.js': 'jsx',
        '.json': 'json',
      },
    },
  },
  server: {
    host: '0.0.0.0',
    fs: {
      allow: [path.resolve(__dirname, '..')],
    },
    proxy: {
      '/api': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
      '/mj': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
      '/pg': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
      '/v1': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
      '/audio-presets': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
      '/playground-samples': {
        target: 'http://localhost:3000',
        changeOrigin: true,
      },
    },
  },
});
