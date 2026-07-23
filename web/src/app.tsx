import { Component, lazy, Suspense, type ErrorInfo, type ReactNode } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { AuthGate } from './auth/auth-screen';
import { SessionProvider } from './auth/session';
import { AppShell } from './components/app-shell';
import { LoadingState } from './components/ui';
import { UpdatePrompt } from './components/update-prompt';
import { RealtimeProvider } from './realtime/realtime';
import { BrowsePage } from './pages/browse';
import { CreatorPage, CreatorsPage } from './pages/creators';
import { GovernancePage } from './pages/governance';
import { JobPage, JobsPage } from './pages/jobs';
import { LibrariesPage } from './pages/libraries';
import { MaintenancePage } from './pages/maintenance';
import { MediaPage } from './pages/media';
import { PublicSharePage } from './pages/public-share';
import { SecurityPage } from './pages/security';
import { WorkPage } from './pages/work';

const RulesPage = lazy(async () => ({ default: (await import('./pages/rules')).RulesPage }));
const RulePackagePage = lazy(async () => ({ default: (await import('./pages/rules')).RulePackagePage }));

export function App() {
  return (
    <ErrorBoundary>
      <SessionProvider>
        <Suspense fallback={<LoadingState label="正在加载页面模块…" />}>
          <Routes>
            <Route path="/share/:credential" element={<PublicSharePage />} />
            <Route
              path="*"
              element={
                <AuthGate>
                  <RealtimeProvider>
                    <Routes>
                      <Route element={<AppShell />}>
                        <Route index element={<Navigate to="/browse" replace />} />
                        <Route path="/browse" element={<BrowsePage />} />
                        <Route path="/works/:workId" element={<WorkPage />} />
                        <Route path="/media/:mediaId" element={<MediaPage />} />
                        <Route path="/creators" element={<CreatorsPage />} />
                        <Route path="/creators/:creatorId" element={<CreatorPage />} />
                        <Route path="/jobs" element={<JobsPage />} />
                        <Route path="/jobs/:jobId" element={<JobPage />} />
                        <Route path="/libraries" element={<LibrariesPage />} />
                        <Route path="/rules" element={<RulesPage />} />
                        <Route path="/rules/:packageId" element={<RulePackagePage />} />
                        <Route path="/governance" element={<GovernancePage />} />
                        <Route path="/security" element={<SecurityPage />} />
                        <Route path="/maintenance" element={<MaintenancePage />} />
                        <Route path="*" element={<Navigate to="/browse" replace />} />
                      </Route>
                    </Routes>
                    <UpdatePrompt />
                  </RealtimeProvider>
                </AuthGate>
              }
            />
          </Routes>
        </Suspense>
      </SessionProvider>
    </ErrorBoundary>
  );
}

class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  override state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  override componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Gallery Web boundary', error.name, info.componentStack);
  }
  override render() {
    if (this.state.error)
      return (
        <main className="fatal-error">
          <h1>Gallery Web 无法继续</h1>
          <p>客户端已停止当前渲染，未向 Source 写入任何内容。</p>
          <ButtonShim onClick={() => location.reload()}>重新加载</ButtonShim>
        </main>
      );
    return this.props.children;
  }
}
function ButtonShim({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <button className="button primary" onClick={onClick}>
      {children}
    </button>
  );
}
