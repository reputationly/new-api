module.exports = {
  root: true,
  env: { browser: true, es2021: true, node: true },
  parserOptions: {
    // 必须 >= 2021：源码里用了数字分隔符（如 2_000_000，见 videoPlayground.
    // constants.js 的录制码率）。停在 2020 时那个文件会 parse error，而 **parse
    // 失败的文件 eslint 是整个跳过的** —— 等于它一条规则都没被检查过。
    ecmaVersion: 2021,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
  },
  plugins: ['header', 'react-hooks'],
  overrides: [
    {
      files: ['**/*.{js,jsx}'],
      rules: {
        // 个人 fork：不强制 QuantumNous 许可证头（见 CLAUDE.md Rule 5 例外）。
        // 现有文件的头保持不变，新文件不再要求加头。
        'header/header': 'off',
        'no-multiple-empty-lines': ['error', { max: 1 }],
        // 用了没 import 的标识符必须在这里被拦下。**vite build 拦不住**：未定义的
        // 自由变量会被当成全局变量打包通过，留到运行时才 ReferenceError。
        // 2026-08-15 就这么炸过一次——常量加了使用点却漏了 import，build 全绿、
        // 一提交就挂（见 commit 38860d141）。全仓开启后只暴露出一个历史死函数，
        // 说明存量代码是干净的，这条规则的维护成本几乎为零。
        'no-undef': 'error',
      },
    },
  ],
};
