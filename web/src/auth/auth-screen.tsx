import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, FieldError, Form, Input, Label, TextField } from 'react-aria-components';
import { useState } from 'react';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from './session';

export function AuthGate({ children }: { children: React.ReactNode }) {
  const { bootstrap } = useSession();
  if (bootstrap.authenticated) return children;
  return <AuthScreen />;
}

function AuthScreen() {
  const { bootstrap, refresh } = useSession();
  const queryClient = useQueryClient();
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const mutation = useMutation({
    mutationFn: async (intent: 'pair' | 'initialize' | 'login') => {
      if (intent === 'pair') {
        const attempt = expectData(
          await api.POST('/api/v1/personal/pairing-attempts', {
            params: { header: csrfHeaders(bootstrap.csrfToken) }
          })
        );
        expectData(
          await api.POST('/api/v1/personal/pair', {
            params: { header: csrfHeaders(bootstrap.csrfToken) },
            body: { credential: attempt.credential }
          })
        );
      } else if (intent === 'initialize') {
        expectData(
          await api.POST('/api/v1/lan/owner', {
            params: { header: csrfHeaders(bootstrap.csrfToken) },
            body: { username, displayName, password }
          })
        );
      } else {
        expectData(
          await api.POST('/api/v1/auth/login', {
            params: { header: csrfHeaders(bootstrap.csrfToken) },
            body: { username, password, clientLabel: 'Gallery Web' }
          })
        );
      }
    },
    onSuccess: async () => {
      setPassword('');
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'bootstrap' });
      await refresh();
    }
  });

  const needsOwner = bootstrap.mode === 'lan' && !bootstrap.lanInitialized;
  return (
    <main className="auth-layout">
      <section className="auth-card" aria-labelledby="auth-title">
        <div className="auth-mark" aria-hidden="true">
          G
        </div>
        <h1 id="auth-title">Gallery · 画廊</h1>
        <p>本地优先的只读媒体目录。认证信息只保存在 HttpOnly Cookie 中。</p>
        {bootstrap.mode === 'personal' ? (
          <Button
            className="button primary wide"
            isPending={mutation.isPending}
            onPress={() => mutation.mutate('pair')}
          >
            在此浏览器完成一次性配对
          </Button>
        ) : (
          <Form
            className="form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              mutation.mutate(needsOwner ? 'initialize' : 'login');
            }}
          >
            <TextField isRequired value={username} onChange={setUsername}>
              <Label>用户名</Label>
              <Input autoComplete="username" />
              <FieldError />
            </TextField>
            {needsOwner && (
              <TextField isRequired value={displayName} onChange={setDisplayName}>
                <Label>显示名称</Label>
                <Input autoComplete="name" />
                <FieldError />
              </TextField>
            )}
            <TextField isRequired minLength={needsOwner ? 10 : 1} value={password} onChange={setPassword}>
              <Label>{needsOwner ? 'Owner 密码（至少 10 字符）' : '密码'}</Label>
              <Input type="password" autoComplete={needsOwner ? 'new-password' : 'current-password'} />
              <FieldError />
            </TextField>
            <Button type="submit" className="button primary wide" isPending={mutation.isPending}>
              {needsOwner ? '初始化 LAN Owner' : '登录'}
            </Button>
          </Form>
        )}
        {needsOwner && (
          <p className="callout warning">
            Owner 初始化只允许在 LAN+loopback 模式执行一次。完成后使用同一账户登录。
          </p>
        )}
        {mutation.isError && (
          <p className="field-error" role="alert">
            {errorMessage(mutation.error)}
          </p>
        )}
      </section>
    </main>
  );
}
