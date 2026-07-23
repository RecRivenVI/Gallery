import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Form, Input, Label, TextField } from 'react-aria-components';
import { useState } from 'react';
import { api, csrfHeaders, errorMessage, expectData, expectNoContent } from '../api/client';
import { useSession } from '../auth/session';
import {
  ConfirmAction,
  DefinitionList,
  EmptyState,
  ErrorState,
  formatDate,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';

export function SecurityPage() {
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const [tokenName, setTokenName] = useState('');
  const [scopeId, setScopeId] = useState('');
  const [secret, setSecret] = useState<string>();
  const [shareScopeKind, setShareScopeKind] = useState<'library' | 'work' | 'media'>('work');
  const [shareScopeId, setShareScopeId] = useState('');
  const [shareSecret, setShareSecret] = useState<string>();
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const users = useQuery({
    queryKey: ['users'],
    enabled: can('users.manage'),
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/users', { signal }))
  });
  const sessions = useQuery({
    queryKey: ['sessions'],
    enabled: can('clients.manage'),
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/sessions', { signal }))
  });
  const tokens = useQuery({
    queryKey: ['tokens'],
    enabled: can('tokens.manage'),
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/api-tokens', { signal }))
  });
  const shares = useQuery({
    queryKey: ['shares'],
    enabled: can('shares.create'),
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/shares', { signal }))
  });
  const audits = useQuery({
    queryKey: ['security-audits'],
    enabled: can('audit.read'),
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/security-audits', { signal }))
  });
  const createToken = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/api-tokens', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: {
            name: tokenName,
            capabilities: ['library.read', 'media.read'],
            scopes: [{ kind: scopeId ? 'library' : 'global', ...(scopeId ? { id: scopeId } : {}) }]
          }
        })
      ),
    onSuccess: async (created) => {
      setSecret(created.secret);
      setTokenName('');
      await client.invalidateQueries({ queryKey: ['tokens'] });
    }
  });
  const createShare = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/shares', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: {
            scopeKind: shareScopeKind,
            scopeId: shareScopeId,
            permissions: ['view'],
            expiresAt: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString()
          }
        })
      ),
    onSuccess: async (created) => {
      setShareSecret(created.secret);
      setShareScopeId('');
      await client.invalidateQueries({ queryKey: ['shares'] });
    }
  });
  const createUser = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/admin/users', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: { username, displayName, password, roles: ['viewer'], grants: [] }
        })
      ),
    onSuccess: async () => {
      setUsername('');
      setDisplayName('');
      setPassword('');
      await client.invalidateQueries({ queryKey: ['users'] });
    }
  });
  const revokeSession = useMutation({
    mutationFn: async (id: string) =>
      expectNoContent(
        await api.DELETE('/api/v1/sessions/{sessionId}', {
          params: { path: { sessionId: id }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['sessions'] })
  });
  const revokeToken = useMutation({
    mutationFn: async (id: string) =>
      expectNoContent(
        await api.DELETE('/api/v1/api-tokens/{tokenId}', {
          params: { path: { tokenId: id }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['tokens'] })
  });
  const revokeShare = useMutation({
    mutationFn: async (id: string) =>
      expectNoContent(
        await api.DELETE('/api/v1/shares/{shareId}', {
          params: { path: { shareId: id }, header: csrfHeaders(bootstrap.csrfToken) }
        })
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['shares'] })
  });
  const pending =
    sessions.isLoading || tokens.isLoading || shares.isLoading || users.isLoading || audits.isLoading;
  const queryError = sessions.error ?? tokens.error ?? shares.error ?? users.error ?? audits.error;
  if (pending) return <LoadingState />;
  if (queryError) return <ErrorState error={queryError} />;
  return (
    <>
      <PageHeader
        title="账户与安全"
        description="会话、Grant、Token 和 Share 的摘要可审计；secret 只显示一次且不会持久化到浏览器。"
      />
      {secret && (
        <section className="one-time-secret" role="alert">
          <h2>请立即保存 Token secret</h2>
          <code>{secret}</code>
          <p>离开或刷新本页后无法再次读取。</p>
          <Button
            className="button secondary"
            onPress={() => {
              void navigator.clipboard.writeText(secret);
            }}
          >
            复制
          </Button>
          <Button className="button ghost" onPress={() => setSecret(undefined)}>
            我已保存
          </Button>
        </section>
      )}
      {shareSecret && (
        <section className="one-time-secret" role="alert">
          <h2>请立即保存分享链接</h2>
          <code>{`${location.origin}/share/${shareSecret}`}</code>
          <p>credential 只显示一次；离开或刷新本页后无法再次读取。</p>
          <Button
            className="button secondary"
            onPress={() => void navigator.clipboard.writeText(`${location.origin}/share/${shareSecret}`)}
          >
            复制链接
          </Button>
          <Button className="button ghost" onPress={() => setShareSecret(undefined)}>
            我已保存
          </Button>
        </section>
      )}
      {can('tokens.manage') && (
        <Form
          className="panel form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            createToken.mutate();
          }}
        >
          <TextField isRequired value={tokenName} onChange={setTokenName}>
            <Label>Token 名称</Label>
            <Input />
          </TextField>
          <TextField value={scopeId} onChange={setScopeId}>
            <Label>可选 Library scope ID</Label>
            <Input />
          </TextField>
          <Button type="submit" className="button primary">
            创建只读 Token
          </Button>
        </Form>
      )}
      {createToken.isError && (
        <p className="field-error" role="alert">
          {errorMessage(createToken.error)}
        </p>
      )}
      {can('shares.create') && (
        <Form
          className="panel form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            createShare.mutate();
          }}
        >
          <label>
            分享范围
            <select
              value={shareScopeKind}
              onChange={(event) => setShareScopeKind(event.target.value as typeof shareScopeKind)}
            >
              <option value="work">Work</option>
              <option value="media">Media</option>
              <option value="library">Library</option>
            </select>
          </label>
          <TextField isRequired value={shareScopeId} onChange={setShareScopeId}>
            <Label>范围 ID</Label>
            <Input />
          </TextField>
          <Button type="submit" className="button primary">
            创建 7 天只读分享
          </Button>
        </Form>
      )}
      {createShare.isError && (
        <p className="field-error" role="alert">
          {errorMessage(createShare.error)}
        </p>
      )}
      {can('users.manage') && (
        <Form
          className="panel form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            createUser.mutate();
          }}
        >
          <TextField isRequired value={username} onChange={setUsername}>
            <Label>新账户用户名</Label>
            <Input autoComplete="off" />
          </TextField>
          <TextField isRequired value={displayName} onChange={setDisplayName}>
            <Label>显示名称</Label>
            <Input />
          </TextField>
          <TextField isRequired value={password} onChange={setPassword}>
            <Label>初始密码</Label>
            <Input type="password" autoComplete="new-password" />
          </TextField>
          <Button type="submit" className="button primary">
            创建 Viewer 账户
          </Button>
        </Form>
      )}
      {createUser.isError && (
        <p className="field-error" role="alert">
          {errorMessage(createUser.error)}
        </p>
      )}
      {users.data && (
        <>
          <h2>本地账户</h2>
          <div className="card-grid">
            {users.data.users.map((user) => (
              <article className="card" key={user.id}>
                <h3>{user.displayName}</h3>
                <StatusBadge tone={user.status === 'active' ? 'success' : 'warning'}>
                  {user.status}
                </StatusBadge>
                <DefinitionList
                  items={[
                    ['用户名', user.username],
                    ['角色', user.roles.join('、')],
                    ['security version', user.securityVersion]
                  ]}
                />
              </article>
            ))}
          </div>
        </>
      )}
      {sessions.data && <h2>服务端 Session</h2>}
      {sessions.data && (
        <div className="card-grid">
          {sessions.data.sessions.map((session) => (
            <article className="card" key={session.id}>
              <h3>{session.clientLabel}</h3>
              <StatusBadge tone={session.revoked ? 'warning' : 'success'}>
                {session.revoked ? '已吊销' : '有效'}
              </StatusBadge>
              <DefinitionList
                items={[
                  ['认证', session.authMethod],
                  ['到期', formatDate(session.expiresAt)],
                  ['最后使用', formatDate(session.lastSeenAt)]
                ]}
              />
              {!session.revoked && (
                <ConfirmAction
                  label="吊销"
                  title="吊销此 Session？"
                  detail="相关 WebSocket 连接会立即失效。"
                  danger
                  onConfirm={async () => {
                    await revokeSession.mutateAsync(session.id);
                  }}
                />
              )}
            </article>
          ))}
        </div>
      )}
      {tokens.data && <h2>API Token</h2>}
      {tokens.data &&
        (tokens.data.tokens.length === 0 ? (
          <EmptyState title="没有 Token" detail="按需创建最小 capability 与 scope 的 Token。" />
        ) : (
          <div className="card-grid">
            {tokens.data.tokens.map((token) => (
              <article className="card" key={token.id}>
                <h3>{token.name}</h3>
                <p>
                  <code>{token.secretPrefix}…</code>
                </p>
                <p>{token.capabilities.join('、')}</p>
                {!token.revoked && (
                  <Button className="button danger" onPress={() => revokeToken.mutate(token.id)}>
                    吊销
                  </Button>
                )}
              </article>
            ))}
          </div>
        ))}
      {shares.data && <h2>匿名分享</h2>}
      {shares.data && (
        <div className="card-grid">
          {shares.data.shares.map((share) => (
            <article className="card" key={share.id}>
              <h3>{share.scopeKind}</h3>
              <StatusBadge>{share.revoked ? '已吊销' : '有效'}</StatusBadge>
              <DefinitionList
                items={[
                  ['范围', share.scopeId],
                  ['权限', share.permissions.join('、')],
                  ['到期', formatDate(share.expiresAt)]
                ]}
              />
              {!share.revoked && (
                <ConfirmAction
                  label="吊销"
                  title="吊销此分享？"
                  detail="已发出的链接会立即失效。"
                  danger
                  onConfirm={async () => revokeShare.mutateAsync(share.id)}
                />
              )}
            </article>
          ))}
        </div>
      )}
      {audits.data && (
        <>
          <h2>安全审计</h2>
          <div className="table-wrap">
            <table className="data-grid">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>动作</th>
                  <th>对象</th>
                  <th>结果</th>
                </tr>
              </thead>
              <tbody>
                {audits.data.audits.slice(0, 100).map((audit) => (
                  <tr key={audit.id}>
                    <td>{formatDate(audit.createdAt)}</td>
                    <td>{audit.action}</td>
                    <td>{audit.targetKind}</td>
                    <td>{audit.outcome}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}
