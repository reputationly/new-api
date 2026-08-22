import '@testing-library/jest-dom/vitest';
// 与 src/index.jsx 的启动路径保持一致。**不是可有可无的**：
// react-i18next 在 i18n 未初始化时，useTranslation() 每次渲染都会返回一个新的 t，
// 而 t 是各个表格组件 columns useMemo 的直接依赖——于是 columns 每次敲键都重建，
// 输入框光标跳到末尾。那是测试环境造出来的假象，生产里 i18n 一启动就初始化了。
// 少了这一行，光标稳定性用例会以「产品有 bug」的样子失败，极易被误判。
import '../i18n/i18n';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  cleanup();
});

// jsdom 没实现下面这几个浏览器 API，而 Semi 的组件（Table 的虚拟滚动、
// Select 的下拉定位、响应式栅格）在挂载时就会直接调用它们。不补的话
// 每个用例都会以 "matchMedia is not a function" 之类的错误开局，
// 与被测逻辑毫无关系。
if (!window.matchMedia) {
  window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  });
}

if (!window.ResizeObserver) {
  window.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

if (!window.IntersectionObserver) {
  window.IntersectionObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  };
}

// Node 22 起自带一个实验性的 localStorage 全局，未加 --localstorage-file 时它是
// undefined，并且会盖掉 jsdom 提供的那份。helpers/api.js 在**模块顶层**就读
// localStorage.getItem('user')，于是任何 import 链碰到 helpers 的测试都在收集阶段挂掉。
function createStorage() {
  let store = new Map();
  return {
    getItem: (k) => (store.has(String(k)) ? store.get(String(k)) : null),
    setItem: (k, v) => store.set(String(k), String(v)),
    removeItem: (k) => store.delete(String(k)),
    clear: () => store.clear(),
    key: (i) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size;
    },
  };
}

for (const name of ['localStorage', 'sessionStorage']) {
  if (!globalThis[name]) {
    Object.defineProperty(globalThis, name, {
      value: createStorage(),
      configurable: true,
      writable: true,
    });
  }
}

// jsdom 的 getContext 恒返回 null。Semi 依赖的 lottie-web 在**模块顶层**就
// canvas.getContext('2d').fillStyle = ... ——拿到 null 直接抛，任何 import 链
// 碰到 Semi 的测试都会在收集阶段崩掉，报错还指向一个与被测逻辑毫无关系的动画库。
//
// 只给一个属性可写的空对象即可：测试里没人真的去断言画布内容。
// 走 alias 换掉 lottie-web 是行不通的——它是 Semi 从 node_modules 里引的，
// 那条路径不经过 vite 的 alias 解析。
HTMLCanvasElement.prototype.getContext = function getContext() {
  return {
    fillRect: () => {},
    clearRect: () => {},
    getImageData: () => ({ data: [] }),
    putImageData: () => {},
    createImageData: () => [],
    setTransform: () => {},
    drawImage: () => {},
    save: () => {},
    restore: () => {},
    beginPath: () => {},
    closePath: () => {},
    stroke: () => {},
    fill: () => {},
    measureText: () => ({ width: 0 }),
    fillText: () => {},
    translate: () => {},
    scale: () => {},
    rotate: () => {},
    arc: () => {},
    moveTo: () => {},
    lineTo: () => {},
  };
};

// Semi 的部分组件用它做动画收尾，jsdom 里没有
if (!window.requestAnimationFrame) {
  window.requestAnimationFrame = (cb) => setTimeout(cb, 0);
  window.cancelAnimationFrame = (id) => clearTimeout(id);
}
