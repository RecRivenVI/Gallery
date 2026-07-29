/*
 * 画廊根组件。
 *
 * 外壳（主题、通知、会话、实时、路由器、SkipLink）由 `main.tsx` 装配，这里只负责画廊
 * 自己的导航结构与路由表。
 *
 * 全屏查看刻意做成**路由**而不是模态框：返回键就是关闭、地址可以直接分享、焦点管理由
 * 页面切换天然承担。这也让"浏览器历史恢复"成为一件自然的事——返回时回到列表，滚动位置
 * 与已加载的页数由 ScrollRestoration 与 TanStack 缓存共同恢复。
 */

import { Link, Route, Routes } from 'react-router-dom';
import { EmptyState } from '../design';
import { AuthGate } from '../shared/session';
import { ConnectionStatus, ScrollRestoration, SignInPanel, TopBar } from './components/chrome';
import { BrowsePage, CreatorPage, CreatorsPage, HomePage, SourcePage } from './pages/discover';
import { FileBrowserPage, FileRootsPage } from './pages/files';
import { ViewerPage } from './pages/viewer';
import { WorkPage } from './pages/work';
import './app.css';

function NotFoundPage() {
  return (
    <div className="gal-page">
      <EmptyState
        title="这个页面不存在"
        description="链接可能已经失效。"
        action={
          <Link className="gal-quicklink" to="/">
            回到画廊首页
          </Link>
        }
      />
    </div>
  );
}

export function GalleryApp() {
  return (
    <AuthGate
      signIn={
        <main id="gallery-main" className="gal-main">
          <SignInPanel />
        </main>
      }
    >
      <div className="gal-shell">
        <TopBar />
        <div className="gal-workspace">
          <ConnectionStatus />
          <ScrollRestoration />
          <main id="gallery-main" className="gal-main">
            <Routes>
              <Route path="/" element={<HomePage />} />
              <Route path="/browse" element={<BrowsePage />} />
              <Route path="/sources/:sourceId" element={<SourcePage />} />
              <Route path="/creators" element={<CreatorsPage />} />
              <Route path="/creators/:creatorId" element={<CreatorPage />} />
              <Route path="/works/:workId" element={<WorkPage />} />
              <Route path="/works/:workId/view/:mediaId" element={<ViewerPage />} />
              <Route path="/files" element={<FileRootsPage />} />
              <Route path="/files/:rootId" element={<FileBrowserPage />} />
              <Route path="*" element={<NotFoundPage />} />
            </Routes>
          </main>
        </div>
      </div>
    </AuthGate>
  );
}
