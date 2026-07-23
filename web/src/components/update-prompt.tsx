import { useRegisterSW } from 'virtual:pwa-register/react';
import { Button } from 'react-aria-components';

export function UpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker
  } = useRegisterSW({ immediate: true });
  if (!needRefresh) return null;
  return (
    <aside className="update-prompt" role="status">
      <strong>Gallery Web 已有新版本</strong>
      <span>更新只替换静态壳，不缓存 API 或媒体正文。</span>
      <Button className="button primary" onPress={() => void updateServiceWorker(true)}>
        立即更新
      </Button>
      <Button className="button ghost" onPress={() => setNeedRefresh(false)}>
        稍后
      </Button>
    </aside>
  );
}
