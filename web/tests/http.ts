/*
 * 单元测试的 HTTP 桩。
 *
 * 必须在**任何**业务模块被求值之前安装，原因有两个，都来自 openapi-fetch 的实现：
 *
 * 1. `createClient()` 在模块加载时就把 `globalThis.fetch` 与 `globalThis.Request` 捕获成
 *    局部变量（`src/api/client.ts` 是模块级单例）。测试里再 `vi.stubGlobal('fetch', …)`
 *    已经太晚，请求会打到真实网络。
 * 2. 客户端的 baseUrl 是空串，因此它构造的是 `new Request('/api/v1/bootstrap')` 这样的
 *    相对 URL。浏览器会用文档地址作为 base，但 vitest 的 jsdom 环境里 `Request` 来自
 *    Node 的 undici，相对 URL 会直接抛 `Failed to parse URL`。
 *
 * 所以这里同时替换 `Request`（补一个 base）与 `fetch`（转发到当前 handler），并由
 * tests/setup.ts 在 setupFiles 阶段调用 installFetchHarness()。
 */

/** 测试地址的 base。任何绝对 URL 断言都应当以它为前缀。 */
export const TEST_ORIGIN = 'http://gallery.test';

export type FetchHandler = (request: Request) => Response | Promise<Response>;

let handler: FetchHandler | null = null;

/** 设置当前用例的响应逻辑。传 null 表示「任何请求都是错误」。 */
export function setFetchHandler(next: FetchHandler | null): void {
  handler = next;
}

export function installFetchHarness(): void {
  const NativeRequest = globalThis.Request;

  class BasedRequest extends NativeRequest {
    constructor(input: RequestInfo | URL, init?: RequestInit) {
      super(typeof input === 'string' ? new URL(input, TEST_ORIGIN) : input, init);
    }
  }

  globalThis.Request = BasedRequest;
  globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = input instanceof NativeRequest ? input : new BasedRequest(input, init);
    if (!handler) {
      return Promise.reject(new TypeError(`测试未设置 fetch handler：${request.method} ${request.url}`));
    }
    return Promise.resolve(handler(request));
  };
}

/** 构造 JSON 响应。 */
export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' }
  });
}

/** 构造服务端结构化错误信封。 */
export function faultResponse(code: string, status: number, correlationId = 'corr-test'): Response {
  return jsonResponse({ error: { code, retryable: status >= 500, correlationId } }, status);
}
