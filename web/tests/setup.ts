import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';
import { installFetchHarness, setFetchHandler } from './http';

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

globalThis.ResizeObserver = ResizeObserverMock;

// 必须在任何业务模块被求值之前替换 fetch/Request，原因见 tests/http.ts 的说明。
installFetchHarness();

// vitest 没有开启 globals，因此 @testing-library/react 不会自动注册 cleanup。
// 不清理会让上一个用例的 DOM 留在页面里，`getByRole` 随即报「找到多个元素」。
afterEach(() => {
  cleanup();
  setFetchHandler(null);
});
