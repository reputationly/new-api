import path from 'path';
import react from '@vitejs/plugin-react';
import { defineConfig, transformWithEsbuild } from 'vite';

// 独立于 vite.config.js：那份带 codeInspectorPlugin 与 vitePluginSemi，
// 前者是开发期的点击跳转注入、后者做 CSS layer 处理，在 jsdom 里都只会添乱。
// 这里只保留跑测试真正需要的两件事：JSX 转换与路径别名。
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  plugins: [
    {
      // src 下有一批 .js 文件里写着 JSX（历史遗留），esbuild 默认按 .js 解析会直接报错
      name: 'treat-js-files-as-jsx',
      async transform(code, id) {
        if (!/src\/.*\.js$/.test(id)) return null;
        return transformWithEsbuild(code, id, {
          loader: 'jsx',
          jsx: 'automatic',
        });
      },
    },
    react(),
  ],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.js'],
    include: ['src/**/*.{test,spec}.{js,jsx}'],
    // e2e 归 Playwright 管，别让 vitest 把它们捡进来
    exclude: ['node_modules', 'dist', 'e2e/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      include: ['src/**/*.{js,jsx}'],
    },
  },
});
