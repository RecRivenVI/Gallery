/*
 * 连接与安全：会话、API Token、分享、本地账户与授权、安全审计。
 *
 * 两条必须由界面兜住的契约事实：
 *
 * 1. **密文只返回一次。** Token 与分享的 secret 只在创建响应里出现一次，服务端只保留前缀。
 *    对话框关闭即丢弃（同时 reset 掉 mutation 缓存），界面里没有任何「再看一次」的入口。
 * 2. **没有角色管理，也没有管理员重置密码。** 角色只能在创建账户时指定，之后只能用
 *    allow/deny 授权调整实际权限。界面不提供这两个并不存在的入口。
 */

import { useState } from 'react';
import { Badge, Button, Checkbox, Select, Tabs, TextInput, useToast } from '../../design';
import { CAPABILITIES, useCapability, useSession, type Capability } from '../../shared/session';
import type { components } from '../../api/schema.gen';
import {
  useApiTokens,
  useCreateApiToken,
  useCreateGrant,
  useCreateLocalUser,
  useCreateShare,
  useGrants,
  useLocalUsers,
  useRevokeApiToken,
  useRevokeGrant,
  useRevokeSession,
  useRevokeShare,
  useSecurityAudits,
  useSessions,
  useSetUserStatus,
  useShares
} from '../api';
import {
  Absent,
  AsyncPanel,
  BoolBadge,
  ConfirmAction,
  ContractNoteList,
  DataTable,
  InlineError,
  MonoId,
  OneTimeSecret,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

type ResourceScope = components['schemas']['ResourceScope'];
type ScopeKind = ResourceScope['kind'];
type UserRole = components['schemas']['LocalUser']['roles'][number];

const SCOPE_KINDS: readonly { id: ScopeKind; label: string }[] = [
  { id: 'global', label: 'global（全局）' },
  { id: 'library', label: 'library（按资料库）' },
  { id: 'source', label: 'source（按来源）' }
];

const ROLES: readonly { id: UserRole; label: string }[] = [
  { id: 'owner', label: 'owner' },
  { id: 'operator', label: 'operator' },
  { id: 'viewer', label: 'viewer' }
];

function hoursFromNow(hours: number): string {
  return new Date(Date.now() + hours * 3_600_000).toISOString();
}

/* ————————————————————————————— 会话 ————————————————————————————— */

function SessionsPanel() {
  const { show } = useToast();
  const sessions = useSessions();
  const revoke = useRevokeSession();
  const canManage = useCapability('clients.manage');

  return (
    <Section
      title="会话"
      description="会话 cookie 是 HttpOnly，浏览器脚本读不到它；这里列出的是服务端记录的活动会话。吊销后对应客户端的 WebSocket 会以 4401 终止且不再重连。"
    >
      <InlineError error={revoke.error} title="吊销未能完成" />
      <AsyncPanel query={sessions}>
        {(data) => (
          <DataTable
            caption="活动会话"
            rows={data.sessions}
            rowKey={(row) => row.id}
            emptyTitle="没有活动会话"
            columns={[
              {
                id: 'revoked',
                header: '状态',
                render: (row) => (
                  <BoolBadge value={!row.revoked} trueLabel="有效" falseLabel="已吊销" falseTone="neutral" />
                )
              },
              { id: 'label', header: '客户端', render: (row) => row.clientLabel },
              {
                id: 'id',
                header: 'Session ID',
                render: (row) => <MonoId value={row.id} label="Session ID" />
              },
              {
                id: 'method',
                header: '认证方式',
                render: (row) => (row.authMethod === 'password' ? '密码' : '一次性配对')
              },
              {
                id: 'principal',
                header: '主体',
                render: (row) => <MonoId value={row.principalId} label="主体 ID" />
              },
              { id: 'created', header: '建立时间', render: (row) => formatDateTime(row.createdAt) },
              { id: 'lastSeen', header: '最后活动', render: (row) => formatDateTime(row.lastSeenAt) },
              { id: 'expires', header: '过期时间', render: (row) => formatDateTime(row.expiresAt) },
              {
                id: 'actions',
                header: '操作',
                render: (row) =>
                  canManage && !row.revoked ? (
                    <ConfirmAction
                      label="吊销"
                      dialogTitle="吊销会话"
                      confirmLabel="确认吊销"
                      description={`吊销后该客户端需要重新认证，其实时连接会立即以 4401 终止。会话 ${row.id}。`}
                      onConfirm={() => {
                        revoke.mutate(row.id, {
                          onSuccess: () => {
                            show({ title: '会话已吊销', tone: 'success' });
                          }
                        });
                      }}
                    />
                  ) : (
                    <Absent />
                  )
              }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

/* ————————————————————————————— capability 与作用域选择 ————————————————————————————— */

function CapabilityPicker({
  selected,
  onToggle
}: {
  selected: ReadonlySet<string>;
  onToggle: (name: Capability, next: boolean) => void;
}) {
  return (
    <fieldset>
      <legend className="manage-facts__term">
        capability（服务端权威词表，共 {CAPABILITIES.length} 项）
      </legend>
      <div className="manage-facts">
        {CAPABILITIES.map((name) => (
          <Checkbox key={name} isSelected={selected.has(name)} onChange={(next) => onToggle(name, next)}>
            {name}
          </Checkbox>
        ))}
      </div>
    </fieldset>
  );
}

function ScopePicker({
  kind,
  id,
  onKindChange,
  onIdChange
}: {
  kind: ScopeKind;
  id: string;
  onKindChange: (kind: ScopeKind) => void;
  onIdChange: (id: string) => void;
}) {
  return (
    <div className="manage-form__row">
      <Select
        label="作用域"
        options={SCOPE_KINDS.map((item) => ({ id: item.id, label: item.label }))}
        selectedKey={kind}
        onSelectionChange={(key) => {
          if (key !== null) onKindChange(key as ScopeKind);
        }}
      />
      {kind === 'global' ? null : (
        <TextInput
          label={kind === 'library' ? 'Library ID' : 'Source ID'}
          value={id}
          onChange={onIdChange}
          description="必须是稳定领域 ID。界面不提供选择器，因为服务端对无权资源会返回 404，列表未必完整。"
        />
      )}
    </div>
  );
}

/* ————————————————————————————— API Token ————————————————————————————— */

function TokensPanel() {
  const { show, dismiss } = useToast();
  const tokens = useApiTokens();
  const create = useCreateApiToken();
  const revoke = useRevokeApiToken();
  const canManage = useCapability('tokens.manage');
  const [name, setName] = useState('');
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [scopeKind, setScopeKind] = useState<ScopeKind>('global');
  const [scopeId, setScopeId] = useState('');
  const [expiryDays, setExpiryDays] = useState('');
  const [secret, setSecret] = useState<string | null>(null);
  const [secretToastId, setSecretToastId] = useState<string | null>(null);

  const days = Number.parseInt(expiryDays, 10);
  const expiryValid = expiryDays === '' || (Number.isFinite(days) && days > 0);
  const scopeValid = scopeKind === 'global' || scopeId.trim() !== '';

  return (
    <Section
      title="API Token"
      description="Token 用于第三方客户端。它的 capability 与作用域是独立的一份，不会因为创建者权限变化而自动变化。"
    >
      <ContractNoteList area="security" only={['security-secret-once']} />
      {canManage ? (
        <div className="manage-form">
          <TextInput label="名称" value={name} onChange={setName} isRequired />
          <CapabilityPicker
            selected={selected}
            onToggle={(capability, next) => {
              setSelected((current) => {
                const draft = new Set(current);
                if (next) draft.add(capability);
                else draft.delete(capability);
                return draft;
              });
            }}
          />
          <ScopePicker kind={scopeKind} id={scopeId} onKindChange={setScopeKind} onIdChange={setScopeId} />
          <TextInput
            label="有效期（天）"
            value={expiryDays}
            onChange={setExpiryDays}
            description="留空表示不设置过期时间。"
            errorMessage={expiryValid ? undefined : '必须是正整数'}
          />
          <div className="manage-form__actions">
            <Button
              variant="primary"
              isPending={create.isPending}
              isDisabled={name.trim() === '' || selected.size === 0 || !expiryValid || !scopeValid}
              onPress={() => {
                create.mutate(
                  {
                    name: name.trim(),
                    capabilities: [...selected],
                    scopes: [
                      scopeKind === 'global' ? { kind: 'global' } : { kind: scopeKind, id: scopeId.trim() }
                    ],
                    ...(expiryDays === '' ? {} : { expiresAt: hoursFromNow(days * 24) })
                  },
                  {
                    onSuccess: (token) => {
                      setSecret(token.secret);
                      setName('');
                      setSelected(new Set());
                      setSecretToastId(
                        show({
                          title: 'Token 已创建',
                          description: '密文只显示这一次。',
                          tone: 'warning',
                          timeoutMs: 0
                        })
                      );
                    }
                  }
                );
              }}
            >
              创建 Token
            </Button>
          </div>
          <InlineError error={create.error} title="Token 未能创建" />
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 tokens.manage，创建入口已隐藏。
        </p>
      )}

      <InlineError error={revoke.error} title="吊销未能完成" />
      <AsyncPanel query={tokens}>
        {(data) => (
          <DataTable
            caption="API Token"
            rows={data.tokens}
            rowKey={(row) => row.id}
            emptyTitle="还没有任何 API Token"
            columns={[
              {
                id: 'revoked',
                header: '状态',
                render: (row) => (
                  <BoolBadge value={!row.revoked} trueLabel="有效" falseLabel="已吊销" falseTone="neutral" />
                )
              },
              { id: 'name', header: '名称', render: (row) => row.name },
              {
                id: 'prefix',
                header: '密文前缀',
                render: (row) => <MonoId value={row.secretPrefix} label="密文前缀" />
              },
              {
                id: 'capabilities',
                header: 'capability',
                render: (row) => row.capabilities.join('、'),
                wrap: true
              },
              {
                id: 'scopes',
                header: '作用域',
                render: (row) =>
                  row.scopes
                    .map((scope) => `${scope.kind}${scope.id === undefined ? '' : `:${scope.id}`}`)
                    .join('、'),
                wrap: true
              },
              { id: 'created', header: '创建时间', render: (row) => formatDateTime(row.createdAt) },
              { id: 'expires', header: '过期时间', render: (row) => formatDateTime(row.expiresAt) },
              { id: 'lastUsed', header: '最后使用', render: (row) => formatDateTime(row.lastUsedAt) },
              {
                id: 'actions',
                header: '操作',
                render: (row) =>
                  canManage && !row.revoked ? (
                    <ConfirmAction
                      label="吊销"
                      dialogTitle="吊销 API Token"
                      confirmLabel="确认吊销"
                      description={`吊销后使用该 Token 的客户端会立即收到 TOKEN_INVALID。Token ${row.name}。`}
                      onConfirm={() => {
                        revoke.mutate(row.id, {
                          onSuccess: () => {
                            show({ title: 'Token 已吊销', tone: 'success' });
                          }
                        });
                      }}
                    />
                  ) : (
                    <Absent />
                  )
              }
            ]}
          />
        )}
      </AsyncPanel>

      <OneTimeSecret
        title="API Token 密文"
        secret={secret}
        description="这是该 Token 的完整密文，服务端只保留前缀。现在就把它保存到你的密码管理器里。"
        onDismiss={() => {
          setSecret(null);
          if (secretToastId !== null) dismiss(secretToastId);
          setSecretToastId(null);
          // 同时清掉 mutation 缓存里的响应，避免密文在内存中比对话框活得更久。
          create.reset();
        }}
      />
    </Section>
  );
}

/* ————————————————————————————— 分享 ————————————————————————————— */

const SHARE_SCOPES = [
  { id: 'library', label: 'library（整个资料库）' },
  { id: 'work', label: 'work（单个作品）' },
  { id: 'media', label: 'media（单个媒体）' }
] as const;

function SharesPanel() {
  const { show, dismiss } = useToast();
  const shares = useShares();
  const create = useCreateShare();
  const revoke = useRevokeShare();
  const canShare = useCapability('shares.create');
  const [scopeKind, setScopeKind] = useState<'library' | 'work' | 'media'>('work');
  const [scopeId, setScopeId] = useState('');
  const [allowDownload, setAllowDownload] = useState(false);
  const [expiryHours, setExpiryHours] = useState('24');
  const [secret, setSecret] = useState<string | null>(null);
  const [secretToastId, setSecretToastId] = useState<string | null>(null);

  const hours = Number.parseInt(expiryHours, 10);
  const expiryValid = Number.isFinite(hours) && hours > 0;

  return (
    <Section
      title="分享"
      description="分享用一次性 credential 暴露只读资源。它绑定创建时的快照语义，不会随后续发布改变可见内容。"
    >
      <ContractNoteList area="security" only={['security-secret-once']} />
      {canShare ? (
        <div className="manage-form">
          <div className="manage-form__row">
            <Select
              label="分享范围"
              options={SHARE_SCOPES.map((item) => ({ id: item.id, label: item.label }))}
              selectedKey={scopeKind}
              onSelectionChange={(key) => {
                if (key !== null) setScopeKind(key as 'library' | 'work' | 'media');
              }}
            />
            <TextInput label="目标 ID" value={scopeId} onChange={setScopeId} isRequired />
            <TextInput
              label="有效期（小时）"
              value={expiryHours}
              onChange={setExpiryHours}
              errorMessage={expiryValid ? undefined : '必须是正整数'}
            />
          </div>
          <Checkbox isSelected={allowDownload} onChange={setAllowDownload}>
            允许下载原始字节（否则只允许查看）
          </Checkbox>
          <div className="manage-form__actions">
            <Button
              variant="primary"
              isPending={create.isPending}
              isDisabled={scopeId.trim() === '' || !expiryValid}
              onPress={() => {
                create.mutate(
                  {
                    scopeKind,
                    scopeId: scopeId.trim(),
                    permissions: allowDownload ? ['view', 'download'] : ['view'],
                    expiresAt: hoursFromNow(hours)
                  },
                  {
                    onSuccess: (share) => {
                      setSecret(share.secret);
                      setScopeId('');
                      setSecretToastId(
                        show({
                          title: '分享已创建',
                          description: '密文只显示这一次。',
                          tone: 'warning',
                          timeoutMs: 0
                        })
                      );
                    }
                  }
                );
              }}
            >
              创建分享
            </Button>
          </div>
          <InlineError error={create.error} title="分享未能创建" />
        </div>
      ) : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 shares.create，创建入口已隐藏。
        </p>
      )}

      <InlineError error={revoke.error} title="吊销未能完成" />
      <AsyncPanel query={shares}>
        {(data) => (
          <DataTable
            caption="分享"
            rows={data.shares}
            rowKey={(row) => row.id}
            emptyTitle="还没有任何分享"
            columns={[
              {
                id: 'revoked',
                header: '状态',
                render: (row) => (
                  <BoolBadge value={!row.revoked} trueLabel="有效" falseLabel="已吊销" falseTone="neutral" />
                )
              },
              { id: 'scope', header: '范围', render: (row) => `${row.scopeKind}:${row.scopeId}` },
              { id: 'permissions', header: '权限', render: (row) => row.permissions.join('、') },
              {
                id: 'prefix',
                header: '密文前缀',
                render: (row) => <MonoId value={row.secretPrefix} label="密文前缀" />
              },
              {
                id: 'fixed',
                header: '固定内容',
                render: (row) =>
                  row.fixedBlobDigest === null || row.fixedBlobDigest === undefined ? (
                    <Absent />
                  ) : (
                    <Badge tone="accent">已固定到具体 Blob</Badge>
                  )
              },
              { id: 'created', header: '创建时间', render: (row) => formatDateTime(row.createdAt) },
              { id: 'expires', header: '过期时间', render: (row) => formatDateTime(row.expiresAt) },
              {
                id: 'actions',
                header: '操作',
                render: (row) =>
                  canShare && !row.revoked ? (
                    <ConfirmAction
                      label="吊销"
                      dialogTitle="吊销分享"
                      confirmLabel="确认吊销"
                      description={`吊销后该链接立即失效。分享 ${row.id}。`}
                      onConfirm={() => {
                        revoke.mutate(row.id, {
                          onSuccess: () => {
                            show({ title: '分享已吊销', tone: 'success' });
                          }
                        });
                      }}
                    />
                  ) : (
                    <Absent />
                  )
              }
            ]}
          />
        )}
      </AsyncPanel>

      <OneTimeSecret
        title="分享 credential"
        secret={secret}
        description="把它拼进分享链接后交给对方。服务端只保留前缀，这里关闭后无法再取回。"
        onDismiss={() => {
          setSecret(null);
          if (secretToastId !== null) dismiss(secretToastId);
          setSecretToastId(null);
          create.reset();
        }}
      />
    </Section>
  );
}

/* ————————————————————————————— 账户与授权 ————————————————————————————— */

function UsersPanel() {
  const { show } = useToast();
  const { mode } = useSession();
  const users = useLocalUsers();
  const create = useCreateLocalUser();
  const setStatus = useSetUserStatus();
  const canManage = useCapability('users.manage');
  const [selectedUser, setSelectedUser] = useState<string | null>(null);
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<UserRole>('viewer');

  return (
    <>
      <Section
        title="本地账户"
        description={
          mode === 'personal'
            ? 'Personal 模式默认只监听 loopback，本地账户主要用于后续启用 LAN。角色只能在创建时指定。'
            : 'LAN 模式下所有访问都要求本地账户。角色只能在创建时指定。'
        }
      >
        <ContractNoteList area="security" only={['security-no-role-management']} />
        {canManage ? (
          <div className="manage-form">
            <div className="manage-form__row">
              <TextInput
                label="用户名"
                value={username}
                onChange={setUsername}
                isRequired
                autoComplete="off"
              />
              <TextInput label="显示名" value={displayName} onChange={setDisplayName} isRequired />
              <TextInput
                label="初始密码"
                type="password"
                value={password}
                onChange={setPassword}
                isRequired
                autoComplete="new-password"
                description="创建后管理员无法替对方重置密码，只能由本人修改。"
              />
              <Select
                label="角色"
                options={ROLES.map((item) => ({ id: item.id, label: item.label }))}
                selectedKey={role}
                onSelectionChange={(key) => {
                  if (key !== null) setRole(key as UserRole);
                }}
              />
            </div>
            <div className="manage-form__actions">
              <Button
                variant="primary"
                isPending={create.isPending}
                isDisabled={username.trim() === '' || displayName.trim() === '' || password === ''}
                onPress={() => {
                  create.mutate(
                    {
                      username: username.trim(),
                      displayName: displayName.trim(),
                      password,
                      roles: [role],
                      grants: []
                    },
                    {
                      onSuccess: (user) => {
                        setUsername('');
                        setDisplayName('');
                        setPassword('');
                        show({ title: `账户 ${user.username} 已创建`, tone: 'success' });
                      }
                    }
                  );
                }}
              >
                创建账户
              </Button>
            </div>
            <InlineError error={create.error} title="账户未能创建" />
          </div>
        ) : (
          <p className="manage-section__description">
            当前主体在 global scope 没有 users.manage，账户入口已隐藏。
          </p>
        )}

        <InlineError error={setStatus.error} title="状态未能变更" />
        <AsyncPanel query={users}>
          {(data) => (
            <DataTable
              caption="本地账户"
              rows={data.users}
              rowKey={(row) => row.id}
              emptyTitle="还没有本地账户"
              emptyDescription="Personal 模式下用一次性配对访问即可；启用 LAN 前需要先初始化 Owner。"
              columns={[
                {
                  id: 'status',
                  header: '状态',
                  render: (row) => (
                    <Badge
                      tone={
                        row.status === 'active'
                          ? 'success'
                          : row.status === 'disabled'
                            ? 'warning'
                            : 'neutral'
                      }
                    >
                      {row.status === 'active' ? '启用' : row.status === 'disabled' ? '已停用' : '已删除'}
                    </Badge>
                  )
                },
                { id: 'username', header: '用户名', render: (row) => row.username },
                { id: 'display', header: '显示名', render: (row) => row.displayName },
                { id: 'roles', header: '角色', render: (row) => row.roles.join('、') },
                { id: 'version', header: '安全版本', render: (row) => row.securityVersion },
                { id: 'created', header: '创建时间', render: (row) => formatDateTime(row.createdAt) },
                {
                  id: 'actions',
                  header: '操作',
                  render: (row) => (
                    <span className="manage-cell-actions">
                      <Button variant="ghost" onPress={() => setSelectedUser(row.id)}>
                        查看授权
                      </Button>
                      {canManage && row.status === 'active' ? (
                        <ConfirmAction
                          label="停用"
                          dialogTitle="停用账户"
                          confirmLabel="确认停用"
                          description={`停用后 ${row.username} 的现有会话会失效，且无法再登录。契约没有「修改角色」端点，停用是唯一的整体收回手段。`}
                          onConfirm={() => {
                            setStatus.mutate(
                              { userId: row.id, status: 'disabled' },
                              {
                                onSuccess: () => {
                                  show({ title: '账户已停用', tone: 'success' });
                                }
                              }
                            );
                          }}
                        />
                      ) : null}
                      {canManage && row.status === 'disabled' ? (
                        <Button
                          variant="secondary"
                          onPress={() => {
                            setStatus.mutate({ userId: row.id, status: 'active' });
                          }}
                        >
                          恢复启用
                        </Button>
                      ) : null}
                    </span>
                  )
                }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>

      <GrantsPanel userId={selectedUser} />
    </>
  );
}

function GrantsPanel({ userId }: { userId: string | null }) {
  const { show } = useToast();
  const grants = useGrants(userId);
  const create = useCreateGrant();
  const revoke = useRevokeGrant();
  const canManage = useCapability('users.manage');
  const [effect, setEffect] = useState<'allow' | 'deny'>('allow');
  const [capability, setCapability] = useState<Capability>('library.read');
  const [scopeKind, setScopeKind] = useState<ScopeKind>('global');
  const [scopeId, setScopeId] = useState('');

  const scopeValid = scopeKind === 'global' || scopeId.trim() !== '';

  return (
    <Section
      title="授权（grant）"
      description="创建账户之后，调整权限的唯一手段就是 allow/deny 授权。deny 优先于 allow，作用域可以是 global、单个资料库或单个来源。"
    >
      {userId === null ? (
        <p className="manage-section__description">在上表点击「查看授权」选择一个账户。</p>
      ) : (
        <>
          {canManage ? (
            <div className="manage-form">
              <div className="manage-form__row">
                <Select
                  label="效果"
                  options={[
                    { id: 'allow', label: 'allow（授予）' },
                    { id: 'deny', label: 'deny（拒绝，优先级更高）' }
                  ]}
                  selectedKey={effect}
                  onSelectionChange={(key) => {
                    if (key !== null) setEffect(key as 'allow' | 'deny');
                  }}
                />
                <Select
                  label="capability"
                  options={CAPABILITIES.map((name) => ({ id: name, label: name }))}
                  selectedKey={capability}
                  onSelectionChange={(key) => {
                    if (key !== null) setCapability(key as Capability);
                  }}
                />
              </div>
              <ScopePicker
                kind={scopeKind}
                id={scopeId}
                onKindChange={setScopeKind}
                onIdChange={setScopeId}
              />
              <div className="manage-form__actions">
                <Button
                  variant="primary"
                  isPending={create.isPending}
                  isDisabled={!scopeValid}
                  onPress={() => {
                    create.mutate(
                      {
                        userId,
                        grant: {
                          effect,
                          capability,
                          scope:
                            scopeKind === 'global'
                              ? { kind: 'global' }
                              : { kind: scopeKind, id: scopeId.trim() }
                        }
                      },
                      {
                        onSuccess: () => {
                          show({ title: '授权已添加', tone: 'success' });
                        }
                      }
                    );
                  }}
                >
                  添加授权
                </Button>
              </div>
              <InlineError error={create.error} title="授权未能添加" />
            </div>
          ) : null}

          <InlineError error={revoke.error} title="授权未能撤销" />
          <AsyncPanel query={grants}>
            {(data) => (
              <DataTable
                caption="账户授权"
                rows={data.grants}
                rowKey={(row) => row.id}
                emptyTitle="该账户没有额外授权"
                emptyDescription="它的权限完全来自创建时指定的角色预设。"
                columns={[
                  {
                    id: 'effect',
                    header: '效果',
                    render: (row) => (
                      <Badge tone={row.effect === 'deny' ? 'danger' : 'success'}>{row.effect}</Badge>
                    )
                  },
                  { id: 'capability', header: 'capability', render: (row) => row.capability },
                  {
                    id: 'scope',
                    header: '作用域',
                    render: (row) =>
                      `${row.scope.kind}${row.scope.id === undefined ? '' : `:${row.scope.id}`}`
                  },
                  {
                    id: 'revoked',
                    header: '状态',
                    render: (row) => (
                      <BoolBadge
                        value={!row.revoked}
                        trueLabel="生效"
                        falseLabel="已撤销"
                        falseTone="neutral"
                      />
                    )
                  },
                  {
                    id: 'actions',
                    header: '操作',
                    render: (row) =>
                      canManage && !row.revoked ? (
                        <ConfirmAction
                          label="撤销"
                          dialogTitle="撤销授权"
                          confirmLabel="确认撤销"
                          description={`撤销 ${row.effect} ${row.capability} 后，该主体的实时连接会以 4403 终止并需要重新认证。`}
                          onConfirm={() => {
                            revoke.mutate(row.id, {
                              onSuccess: () => {
                                show({ title: '授权已撤销', tone: 'success' });
                              }
                            });
                          }}
                        />
                      ) : (
                        <Absent />
                      )
                  }
                ]}
              />
            )}
          </AsyncPanel>
        </>
      )}
    </Section>
  );
}

/* ————————————————————————————— 审计 ————————————————————————————— */

function AuditsPanel() {
  const audits = useSecurityAudits();
  return (
    <Section
      title="安全审计"
      description="服务端记录的安全相关动作。契约没有任何查询参数，返回的就是服务端决定的那一段。"
    >
      <ContractNoteList area="security" only={['security-audit-no-filter']} />
      <AsyncPanel query={audits}>
        {(data) => (
          <DataTable
            caption="安全审计"
            rows={data.audits}
            rowKey={(row) => row.id}
            emptyTitle="还没有审计记录"
            columns={[
              { id: 'created', header: '时间', render: (row) => formatDateTime(row.createdAt) },
              { id: 'action', header: '动作', render: (row) => row.action },
              {
                id: 'outcome',
                header: '结果',
                render: (row) => (
                  <Badge tone={row.outcome === 'success' ? 'success' : 'danger'}>{row.outcome}</Badge>
                )
              },
              {
                id: 'actor',
                header: '主体',
                render: (row) => <MonoId value={row.actorId} label="主体 ID" />
              },
              { id: 'target', header: '目标', render: (row) => `${row.targetKind}:${row.targetId}` },
              { id: 'detail', header: '细节', render: (row) => JSON.stringify(row.detail), wrap: true }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

export function SecurityPage() {
  return (
    <>
      <PageHeader
        title="连接与安全"
        lead="会话、API Token、分享、本地账户与授权都在这里。所有密文只出现一次；角色只能在创建账户时指定，之后只能通过授权调整。"
      />
      <Tabs
        label="安全管理分区"
        defaultSelectedKey="sessions"
        items={[
          { id: 'sessions', label: '会话', content: <SessionsPanel /> },
          { id: 'tokens', label: 'API Token', content: <TokensPanel /> },
          { id: 'shares', label: '分享', content: <SharesPanel /> },
          { id: 'users', label: '账户与授权', content: <UsersPanel /> },
          { id: 'audits', label: '安全审计', content: <AuditsPanel /> }
        ]}
      />
    </>
  );
}
