/*
 * 管理端的查询与变更。
 *
 * 三条约定：
 *
 * 1. **queryKey 的第一段必须来自 `shared/query.ts` 的 SNAPSHOT_QUERY_PREFIXES**，否则
 *    WebSocket 重连、序号缺口或 `connection.ready` 触发的「重新拉快照」不会覆盖它。
 *    唯一的例外是 `security`：安全资源不在该表里，因此 `layout.tsx` 显式订阅
 *    `session.revoked` / `grant.revoked` 并按快照代次失效它，注释见那里。
 * 2. **所有变更都带 CSRF header**，取值来自 bootstrap；认证状态变化后 token 会变，
 *    `useCsrfHeaders()` 每次渲染都取最新值。
 * 3. **变更绝不自动重试**（`shared/query.ts` 已设 `retry: false`）：重复提交可能产生
 *    第二个 Job 或第二条不可重建的用户事实。
 */

import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueries,
  useQueryClient,
  type UseMutationResult
} from '@tanstack/react-query';
import { useCallback } from 'react';
import { api, expectData, expectNoContent } from '../api/client';
import type { components } from '../api/schema.gen';
import { useCsrfHeaders } from '../shared/session';
import { isRecord, parseRuleValue } from './rules/lossless';

type Schemas = components['schemas'];

export type Job = Schemas['Job'];
export type JobAttempt = Schemas['JobAttempt'];
export type JobStatus = Job['status'];
export type JobType = Job['type'];
export type ScanProfile = NonNullable<Schemas['ScanJobCreateRequest']['scanProfile']>;
export type Source = Schemas['Source'];
export type SourceScanState = Schemas['SourceScanState'];
export type Library = Schemas['Library'];
export type QueryPublication = Schemas['QueryPublication'];
export type HealthResponse = Schemas['HealthResponse'];
export type ControlBackupManifest = Schemas['ControlBackupManifest'];
export type ControlRestoreReport = Schemas['ControlRestoreReport'];
export type MaintenanceJobResponse = Schemas['MaintenanceJobResponse'];
export type SessionSummary = Schemas['SessionSummary'];
export type APIToken = Schemas['APIToken'];
export type APITokenCreated = Schemas['APITokenCreated'];
export type Share = Schemas['Share'];
export type ShareCreated = Schemas['ShareCreated'];
export type LocalUser = Schemas['LocalUser'];
export type AuthorizationGrant = Schemas['AuthorizationGrant'];
export type AuthorizationGrantInput = Schemas['AuthorizationGrantInput'];
export type SecurityAudit = Schemas['SecurityAudit'];
export type RulePackage = Schemas['RulePackage'];
export type RuleDraft = Schemas['RuleDraft'];
export type RuleDraftValidationResult = Schemas['RuleDraftValidationResult'];
export type RuleVersion = Schemas['RuleVersion'];
export type RuleVersionDiff = Schemas['RuleVersionDiff'];
export type RuleImpactResult = Schemas['RuleImpactResult'];
export type RuleDebugRequest = Schemas['RuleDebugRequest'] & {
  package: NonNullable<Schemas['RuleDebugRequest']['package']>;
};
export type RuleDryRunSample = Schemas['RuleDryRunSample'];
export type RuleDryRunResult = Schemas['RuleDryRunResult'];
export type RuleExplainResult = Schemas['RuleExplainResult'];
export type RuleTraceResult = Record<string, unknown>;
export type RuleParameterSet = Schemas['RuleParameterSet'];
export type SourceRuleBinding = Schemas['SourceRuleBinding'];
export type BindingIssue = Schemas['BindingIssue'];
export type SourceStructureDecision = Schemas['SourceStructureDecision'];
export type OrphanCandidate = Schemas['OrphanCandidate'];
export type OrphanDecisionResult = Schemas['OrphanDecisionResult'];
export type BindingActionResult = Schemas['BindingActionResult'];

/* ————————————————————————————— 基础设施 ————————————————————————————— */

/** 治理列表每页条数。服务端 keyset 游标只支持逐页向后，没有跳页。 */
export const GOVERNANCE_PAGE_SIZE = 50;

function useInvalidate(): (prefixes: readonly string[]) => void {
  const queryClient = useQueryClient();
  return useCallback(
    (prefixes: readonly string[]) => {
      for (const prefix of prefixes) {
        void queryClient.invalidateQueries({ queryKey: [prefix] });
      }
    },
    [queryClient]
  );
}

/** 幂等键。扫描创建支持它，用来避免网络重发导致第二个扫描 Job。 */
export function newIdempotencyKey(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `idem-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/* ————————————————————————————— 概览与诊断 ————————————————————————————— */

/**
 * 健康检查。
 *
 * 只有两个数据库的 ok / 非 ok，没有磁盘、队列、Watcher 或外部工具状态；它返回 ok
 * 不代表扫描或维护一定可用。
 */
export function useHealth() {
  return useQuery({
    queryKey: ['maintenance', 'health'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/health', { signal }))
  });
}

export function useCurrentPublication() {
  return useQuery({
    queryKey: ['publication', 'current'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/query-publications/current', { signal }))
  });
}

/* ————————————————————————————— 资源 ————————————————————————————— */

export function useLibraries() {
  return useQuery({
    queryKey: ['libraries', 'list'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/libraries', { signal }))
  });
}

export function useSources() {
  return useQuery({
    queryKey: ['sources', 'list'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/sources', { signal }))
  });
}

export function useSourceScanStatus(sourceId: string | null) {
  return useQuery({
    queryKey: ['sources', 'scan-status', sourceId],
    enabled: sourceId !== null,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/sources/{sourceId}/scan-status', {
          params: { path: { sourceId: sourceId ?? '' } },
          signal
        })
      )
  });
}

export type CreateLibraryInput = Schemas['LibraryCreateRequest'];

export function useCreateLibrary(): UseMutationResult<Library, unknown, CreateLibraryInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateLibraryInput) =>
      expectData(await api.POST('/api/v1/libraries', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['libraries', 'sources']);
    }
  });
}

export type CreateSourceInput = Schemas['SourceCreateRequest'];

export function useCreateSource(): UseMutationResult<Source, unknown, CreateSourceInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    gcTime: 0,
    mutationFn: async (input: CreateSourceInput) =>
      expectData(await api.POST('/api/v1/sources', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['sources']);
    }
  });
}

/* ————————————————————————————— 任务 ————————————————————————————— */

/**
 * 任务快照。
 *
 * `GET /api/v1/jobs` 只接受 status 与 limit——**契约里没有 cursor**，因此更早的历史无法翻页。
 * 这是任务状态的**权威**来源：WebSocket 的 job.* 事件只是「去重新问一次」的提示。
 */
export function useJobs(status: string | null, limit: number) {
  return useQuery({
    queryKey: ['jobs', 'list', status, limit],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/jobs', {
          params: { query: status === null ? { limit } : { status, limit } },
          signal
        })
      )
  });
}

export function useJob(jobId: string | undefined) {
  return useQuery({
    queryKey: ['jobs', 'detail', jobId],
    enabled: jobId !== undefined,
    queryFn: async ({ signal }) =>
      expectData(await api.GET('/api/v1/jobs/{jobId}', { params: { path: { jobId: jobId ?? '' } }, signal }))
  });
}

export function useJobAttempts(jobId: string | undefined) {
  return useQuery({
    queryKey: ['jobs', 'attempts', jobId],
    enabled: jobId !== undefined,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/jobs/{jobId}/attempts', {
          params: { path: { jobId: jobId ?? '' } },
          signal
        })
      )
  });
}

export function useCancelJob(): UseMutationResult<Job, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (jobId: string) =>
      expectData(await api.POST('/api/v1/jobs/{jobId}/cancel', { params: { header, path: { jobId } } })),
    onSuccess: () => {
      invalidate(['jobs']);
    }
  });
}

/** 重试在**同一个 Job ID** 上开新 Attempt，不会产生新的任务。 */
export function useRetryJob(): UseMutationResult<Job, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (jobId: string) =>
      expectData(await api.POST('/api/v1/jobs/{jobId}/retry', { params: { header, path: { jobId } } })),
    onSuccess: () => {
      invalidate(['jobs']);
    }
  });
}

export interface CreateScanInput {
  sourceId: string;
  scanProfile: ScanProfile;
  idempotencyKey: string;
}

/**
 * 创建扫描 Job。
 *
 * `index` 档案只对首次扫描有效：Source 已发布或已有持久历史后显式请求 index 会被稳定拒绝为
 * 409 CONFLICT。**绝不能在这里捕获该冲突并改成 incremental 重发**——那会让用户以为执行的是
 * 快速索引，实际却做了另一件事。冲突原样抛给调用方，由页面如实解释。
 */
export function useCreateScanJob(): UseMutationResult<Job, unknown, CreateScanInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateScanInput) =>
      expectData(
        await api.POST('/api/v1/sources/{sourceId}/scan-jobs', {
          params: {
            header: { ...header, 'Idempotency-Key': input.idempotencyKey },
            path: { sourceId: input.sourceId }
          },
          body: { scanProfile: input.scanProfile }
        })
      ),
    onSuccess: () => {
      invalidate(['jobs', 'sources']);
    }
  });
}

/* ————————————————————————————— 维护、备份与恢复 ————————————————————————————— */

export type MaintenanceKind = 'gc' | 'checkpoint' | 'vacuum';

export interface MaintenanceInput {
  kind: MaintenanceKind;
  retentionSeconds: number;
  dryRun: boolean;
}

export function useRunMaintenance(): UseMutationResult<MaintenanceJobResponse, unknown, MaintenanceInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: MaintenanceInput) => {
      if (input.kind === 'gc') {
        return expectData(
          await api.POST('/api/v1/admin/maintenance/gc', {
            params: { header },
            body: { retentionSeconds: input.retentionSeconds, dryRun: input.dryRun }
          })
        );
      }
      if (input.kind === 'checkpoint') {
        return expectData(await api.POST('/api/v1/admin/maintenance/checkpoint', { params: { header } }));
      }
      return expectData(await api.POST('/api/v1/admin/maintenance/vacuum', { params: { header } }));
    },
    onSuccess: () => {
      invalidate(['jobs', 'maintenance']);
    }
  });
}

/** 备份清单列表。返回的是 manifest，**不是字节**：契约里既没有下载也没有上传端点。 */
export function useControlBackups() {
  return useQuery({
    queryKey: ['maintenance', 'control-backups'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/control-backups', { signal }))
  });
}

export function useCreateControlBackup(): UseMutationResult<Job, unknown, void> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async () =>
      expectData(await api.POST('/api/v1/admin/control-backups', { params: { header } })),
    onSuccess: () => {
      invalidate(['jobs', 'maintenance']);
    }
  });
}

/** 恢复干跑。只产生结论，不改变任何东西。 */
export function useVerifyControlRestore(): UseMutationResult<ControlRestoreReport, unknown, string> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (backupId: string) =>
      expectData(
        await api.POST('/api/v1/admin/control-restores/verify', { params: { header }, body: { backupId } })
      )
  });
}

/** 登记恢复请求。**下次启动才应用**，并且没有任何事件会通知重启状态。 */
export function useRequestControlRestore(): UseMutationResult<
  Schemas['ControlRestoreRequestResponse'],
  unknown,
  string
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (backupId: string) =>
      expectData(
        await api.POST('/api/v1/admin/control-restores', { params: { header }, body: { backupId } })
      ),
    onSuccess: () => {
      invalidate(['maintenance']);
    }
  });
}

/* ————————————————————————————— 安全 ————————————————————————————— */

export function useSessions() {
  return useQuery({
    queryKey: ['security', 'sessions'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/sessions', { signal }))
  });
}

export function useRevokeSession(): UseMutationResult<void, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (sessionId: string) => {
      expectNoContent(
        await api.DELETE('/api/v1/sessions/{sessionId}', { params: { header, path: { sessionId } } })
      );
    },
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useApiTokens() {
  return useQuery({
    queryKey: ['security', 'api-tokens'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/api-tokens', { signal }))
  });
}

export interface CreateApiTokenInput {
  name: string;
  capabilities: string[];
  scopes: Schemas['ResourceScope'][];
  expiresAt?: string;
}

export function useCreateApiToken(): UseMutationResult<APITokenCreated, unknown, CreateApiTokenInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateApiTokenInput) =>
      expectData(await api.POST('/api/v1/api-tokens', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useRevokeApiToken(): UseMutationResult<void, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (tokenId: string) => {
      expectNoContent(
        await api.DELETE('/api/v1/api-tokens/{tokenId}', { params: { header, path: { tokenId } } })
      );
    },
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useShares() {
  return useQuery({
    queryKey: ['security', 'shares'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/shares', { signal }))
  });
}

export type ShareCreateRequest = Schemas['ShareCreateRequest'];

export function useCreateShare(): UseMutationResult<ShareCreated, unknown, ShareCreateRequest> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: ShareCreateRequest) =>
      expectData(await api.POST('/api/v1/shares', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useRevokeShare(): UseMutationResult<void, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (shareId: string) => {
      expectNoContent(
        await api.DELETE('/api/v1/shares/{shareId}', { params: { header, path: { shareId } } })
      );
    },
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useLocalUsers() {
  return useQuery({
    queryKey: ['security', 'users'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/users', { signal }))
  });
}

export type LocalUserCreateRequest = Schemas['LocalUserCreateRequest'];

/**
 * 创建本地账户。
 *
 * 角色**只能**在这里指定：契约里没有修改角色的端点，也没有管理员替他人重置密码的端点。
 * 事后只能通过 allow/deny 授权（grant）调整实际权限。
 */
export function useCreateLocalUser(): UseMutationResult<LocalUser, unknown, LocalUserCreateRequest> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: LocalUserCreateRequest) =>
      expectData(await api.POST('/api/v1/admin/users', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export interface SetUserStatusInput {
  userId: string;
  status: Schemas['UserStatusRequest']['status'];
}

export function useSetUserStatus(): UseMutationResult<void, unknown, SetUserStatusInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: SetUserStatusInput) => {
      expectNoContent(
        await api.PATCH('/api/v1/admin/users/{userId}/status', {
          params: { header, path: { userId: input.userId } },
          body: { status: input.status }
        })
      );
    },
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useGrants(userId: string | null) {
  return useQuery({
    queryKey: ['security', 'grants', userId],
    enabled: userId !== null,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/admin/users/{userId}/grants', {
          params: { path: { userId: userId ?? '' } },
          signal
        })
      )
  });
}

export interface CreateGrantInput {
  userId: string;
  grant: AuthorizationGrantInput;
}

export function useCreateGrant(): UseMutationResult<AuthorizationGrant, unknown, CreateGrantInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateGrantInput) =>
      expectData(
        await api.POST('/api/v1/admin/users/{userId}/grants', {
          params: { header, path: { userId: input.userId } },
          body: input.grant
        })
      ),
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

export function useRevokeGrant(): UseMutationResult<void, unknown, string> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (grantId: string) => {
      expectNoContent(
        await api.DELETE('/api/v1/admin/grants/{grantId}', { params: { header, path: { grantId } } })
      );
    },
    onSuccess: () => {
      invalidate(['security']);
    }
  });
}

/** 安全审计。契约**没有任何查询参数**：返回的就是服务端决定的那一段。 */
export function useSecurityAudits() {
  return useQuery({
    queryKey: ['security', 'audits'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/security-audits', { signal }))
  });
}

/* ————————————————————————————— 规则 ————————————————————————————— */

export function useRulePackages() {
  return useQuery({
    queryKey: ['rules', 'packages'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/rule-packages', { signal }))
  });
}

export function useRulePackage(packageId: string | undefined) {
  return useQuery({
    queryKey: ['rules', 'package', packageId],
    enabled: packageId !== undefined,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}', {
          params: { path: { packageId: packageId ?? '' } },
          signal
        })
      )
  });
}

export function useCreateRulePackage(): UseMutationResult<
  RulePackage,
  unknown,
  { name: string; ruleSetId?: string; description?: string }
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: { name: string; ruleSetId?: string; description?: string }) =>
      expectData(await api.POST('/api/v1/rule-packages', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

export function useRuleDraft(packageId: string | undefined) {
  return useQuery({
    queryKey: ['rules', 'draft', packageId],
    enabled: packageId !== undefined,
    // 草稿是并发编辑的对象：每次进入都必须拿到最新 revision，否则第一次保存必然 409。
    staleTime: 0,
    retry: false,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}/draft', {
          params: { path: { packageId: packageId ?? '' } },
          signal
        })
      )
  });
}

export interface SaveRuleDraftInput {
  packageId: string;
  /** JSON 也以原始文本提交，避免任何数字先经过 JavaScript Number。 */
  content: string;
  format: RuleDraft['format'];
  /** GET 草稿返回的 revision。服务端用它做 CAS，冲突返回 409 RULE_DRAFT_CONFLICT。 */
  expectedRevision: number;
  baseSemanticHash?: string;
}

/**
 * 保存草稿。
 *
 * `If-Match` 带的是**草稿**的 revision（不是规则包的）。其他会话已经改过草稿时服务端返回
 * 409 RULE_DRAFT_CONFLICT，此处不做任何自动重试或强制覆盖——覆盖别人的编辑是不可接受的。
 */
export function useSaveRuleDraft(): UseMutationResult<RuleDraft, unknown, SaveRuleDraftInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: SaveRuleDraftInput) =>
      expectData(
        await api.PUT('/api/v1/rule-packages/{packageId}/draft', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { packageId: input.packageId }
          },
          body: {
            content: input.content,
            format: input.format,
            ...(input.baseSemanticHash === undefined ? {} : { baseSemanticHash: input.baseSemanticHash })
          }
        })
      ),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

export interface RuleDraftRevisionInput {
  packageId: string;
  expectedRevision: number;
}

/** 校验草稿。契约要求 `rules.write`——只读分析却要写权限，是已知的过度限制。 */
export function useValidateRuleDraft(): UseMutationResult<
  RuleDraftValidationResult,
  unknown,
  RuleDraftRevisionInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: RuleDraftRevisionInput) =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/draft/validate', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { packageId: input.packageId }
          }
        })
      ),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

export interface PublishRuleDraftInput {
  packageId: string;
  expectedRevision: number;
  reason: string;
  confirmImpact: boolean;
}

/** 发布草稿。仍有未通过的校验或未确认的影响时返回 409 RULE_PUBLISH_BLOCKED。 */
export function usePublishRuleDraft(): UseMutationResult<RuleVersion, unknown, PublishRuleDraftInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: PublishRuleDraftInput) =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/publish', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { packageId: input.packageId }
          },
          body: {
            reason: input.reason,
            expectedRevision: input.expectedRevision,
            confirmImpact: input.confirmImpact
          }
        })
      ),
    onSuccess: () => {
      invalidate(['rules', 'jobs']);
    }
  });
}

export interface RollbackInput {
  packageId: string;
  targetSemanticHash: string;
  expectedRevision: number;
  reason: string;
  confirmImpact: boolean;
}

/**
 * 只回滚规则包的 current 指针。它不会改写既有 Binding、ParameterSet 或 Job，也不会自动创建任务。
 * diff 所说的重扫/重投影只是调用方需要另行安排的后续动作。
 */
export function useRollbackRulePackage(): UseMutationResult<RuleVersion, unknown, RollbackInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: RollbackInput) =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/rollback', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { packageId: input.packageId }
          },
          body: {
            targetSemanticHash: input.targetSemanticHash,
            expectedRevision: input.expectedRevision,
            reason: input.reason,
            confirmImpact: input.confirmImpact
          }
        })
      ),
    onSettled: () => {
      // 失败也刷新：revision 冲突后页面必须看到新的 current，而 mutation error 仍常驻在页面内。
      invalidate(['rules']);
    }
  });
}

export function useRuleVersions(packageId: string | undefined) {
  return useQuery({
    queryKey: ['rules', 'versions', packageId],
    enabled: packageId !== undefined,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}/versions', {
          params: { path: { packageId: packageId ?? '' } },
          signal
        })
      )
  });
}

/**
 * 为 Binding 采纳选择并行读取多个 active RulePackage 的全部不可变版本。
 *
 * Package current 只是作者工作流指针；后端允许采用同一 active Package 下任意仍为
 * published + executable 的历史版本，因此不能把这里静默缩窄为 current。
 */
export function useRuleVersionsForPackages(packageIds: readonly string[]) {
  const results = useQueries({
    queries: packageIds.map((packageId) => ({
      queryKey: ['rules', 'versions', packageId],
      queryFn: async ({ signal }: { signal: AbortSignal }) =>
        expectData(
          await api.GET('/api/v1/rule-packages/{packageId}/versions', {
            params: { path: { packageId } },
            signal
          })
        )
    }))
  });
  const isPending = results.some((result) => result.isPending);
  const errorResult = results.find((result) => result.isError);
  const fetchStatus: 'fetching' | 'paused' | 'idle' = results.some(
    (result) => result.fetchStatus === 'fetching'
  )
    ? 'fetching'
    : results.some((result) => result.fetchStatus === 'paused')
      ? 'paused'
      : 'idle';
  return {
    isPending,
    isError: errorResult !== undefined,
    error: errorResult?.error,
    data:
      isPending || errorResult !== undefined
        ? undefined
        : { items: results.flatMap((result) => result.data?.items ?? []) },
    fetchStatus,
    refetch: () => Promise.all(results.map((result) => result.refetch()))
  };
}

/** 持久 RuleVersion 的结构化差异；rollback 的确认只能绑定到这份证据。 */
export function useRuleVersionDiff(oldSemanticHash: string | null, newSemanticHash: string | null) {
  const header = useCsrfHeaders();
  return useQuery({
    queryKey: ['rules', 'version-diff', oldSemanticHash, newSemanticHash],
    enabled: oldSemanticHash !== null && newSemanticHash !== null,
    retry: false,
    queryFn: async ({ signal }) =>
      expectData(
        await api.POST('/api/v1/rule-versions/diff', {
          params: { header },
          body: {
            oldSemanticHash: oldSemanticHash ?? '',
            newSemanticHash: newSemanticHash ?? ''
          },
          signal
        })
      )
  });
}

export interface DeprecateRulePackageInput {
  packageId: string;
  expectedRevision: number;
  reason: string;
}

/** 单向弃用规则包；既有版本、ParameterSet、Binding 与 Job 保持原事实。 */
export function useDeprecateRulePackage(): UseMutationResult<
  RulePackage,
  unknown,
  DeprecateRulePackageInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: DeprecateRulePackageInput) =>
      expectData(
        await api.POST('/api/v1/rule-packages/{packageId}/deprecate', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { packageId: input.packageId }
          },
          body: { expectedRevision: input.expectedRevision, reason: input.reason }
        })
      ),
    onSettled: () => {
      invalidate(['rules']);
    }
  });
}

export interface DeprecateRuleVersionInput {
  semanticHash: string;
  reason: string;
}

/** 弃用非 current、且未被 active SourceRuleBinding 使用的不可变 RuleVersion。 */
export function useDeprecateRuleVersion(): UseMutationResult<
  RuleVersion,
  unknown,
  DeprecateRuleVersionInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: DeprecateRuleVersionInput) =>
      expectData(
        await api.POST('/api/v1/rule-versions/{semanticHash}/deprecate', {
          params: { header, path: { semanticHash: input.semanticHash } },
          body: { reason: input.reason }
        })
      ),
    onSettled: () => {
      invalidate(['rules']);
    }
  });
}

export function useRuleAudits(packageId: string | undefined) {
  return useQuery({
    queryKey: ['rules', 'audits', packageId],
    enabled: packageId !== undefined,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-packages/{packageId}/audits', {
          params: { path: { packageId: packageId ?? '' } },
          signal
        })
      )
  });
}

export function useRuleParameterSets(
  /** null 禁用查询；undefined 列出全部版本的参数集。 */
  semanticHash: string | null | undefined,
  status?: RuleParameterSet['status']
) {
  return useQuery({
    queryKey: ['rules', 'parameter-sets', semanticHash, status],
    enabled: semanticHash !== null,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-parameters', {
          params: {
            query: {
              ...(semanticHash === null || semanticHash === undefined ? {} : { semanticHash }),
              ...(status === undefined ? {} : { status })
            }
          },
          signal
        })
      )
  });
}

export interface CreateRuleParameterSetInput {
  name: string;
  semanticHash: string;
  /** 规范 JSON 对象文本，数字不得经过 JavaScript Number。 */
  parameters: string;
}

export function useCreateRuleParameterSet(): UseMutationResult<
  RuleParameterSet,
  unknown,
  CreateRuleParameterSetInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateRuleParameterSetInput) =>
      expectData(
        await api.POST('/api/v1/rule-parameters', {
          params: { header },
          body: input
        })
      ),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

export interface ImpactRuleParameterSetInput {
  parameterId: string;
  /** 规范 JSON 对象文本，数字不得经过 JavaScript Number。 */
  parameters: string;
}

export function useImpactRuleParameterSet(): UseMutationResult<
  RuleImpactResult,
  unknown,
  ImpactRuleParameterSetInput
> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (input: ImpactRuleParameterSetInput) =>
      expectData(
        await api.POST('/api/v1/rule-parameters/{parameterId}/impact', {
          params: { header, path: { parameterId: input.parameterId } },
          body: { parameters: input.parameters }
        })
      )
  });
}

export interface UpdateRuleParameterSetInput {
  parameterId: string;
  parameters: string;
  expectedRevision: number;
  confirmImpact: boolean;
}

export function useUpdateRuleParameterSet(): UseMutationResult<
  RuleParameterSet,
  unknown,
  UpdateRuleParameterSetInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: UpdateRuleParameterSetInput) =>
      expectData(
        await api.PUT('/api/v1/rule-parameters/{parameterId}', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { parameterId: input.parameterId }
          },
          body: {
            parameters: input.parameters,
            expectedRevision: input.expectedRevision,
            confirmImpact: input.confirmImpact
          }
        })
      ),
    onSettled: () => {
      // 冲突时刷新服务器 revision；编辑器自行保留本地文本，不静默采用缓存值。
      invalidate(['rules', 'sources']);
    }
  });
}

export interface CopyRuleParameterSetInput {
  parameterId: string;
  name: string;
}

export function useCopyRuleParameterSet(): UseMutationResult<
  RuleParameterSet,
  unknown,
  CopyRuleParameterSetInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CopyRuleParameterSetInput) =>
      expectData(
        await api.POST('/api/v1/rule-parameters/{parameterId}/copy', {
          params: { header, path: { parameterId: input.parameterId } },
          body: { name: input.name }
        })
      ),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

export interface DeprecateRuleParameterSetInput {
  parameterId: string;
  expectedRevision: number;
  reason: string;
}

export function useDeprecateRuleParameterSet(): UseMutationResult<
  RuleParameterSet,
  unknown,
  DeprecateRuleParameterSetInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: DeprecateRuleParameterSetInput) =>
      expectData(
        await api.POST('/api/v1/rule-parameters/{parameterId}/deprecate', {
          params: {
            header: { ...header, 'If-Match': `"${String(input.expectedRevision)}"` },
            path: { parameterId: input.parameterId }
          },
          body: { expectedRevision: input.expectedRevision, reason: input.reason }
        })
      ),
    onSettled: () => {
      invalidate(['rules']);
    }
  });
}

export interface ImpactInput {
  before: string | null;
  after: string;
}

/** 变更影响评估。发布前必须展示它：它决定要不要重扫、重投影、重建搜索或重建派生资源。 */
export function useRuleImpact(): UseMutationResult<RuleImpactResult, unknown, ImpactInput> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (input: ImpactInput) =>
      expectData(
        await api.POST('/api/v1/rules/impact', {
          params: { header },
          body: input
        })
      )
  });
}

export interface RuleDebugInput {
  /** 与 exactBody 对应的类型化请求；实际发送字节由 exactBody 决定。 */
  request: RuleDebugRequest;
  /** 无损 JSON 拼装的完整请求体，避免 package/parameters/sample 中的数字经过 Number。 */
  exactBody: string;
}

function exactRuleDebugBody(input: RuleDebugInput): string {
  return input.exactBody;
}

function parseExactRuleDebugResponse(text: string): Record<string, unknown> {
  const value = parseRuleValue(text);
  if (!isRecord(value)) throw new TypeError('规则调试响应必须是 JSON 对象');
  return value;
}

/** 对当前规则包与显式合成输入执行受限 Dry Run。 */
export function useRuleDryRun(): UseMutationResult<RuleDryRunResult, unknown, RuleDebugInput> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (input: RuleDebugInput) =>
      parseExactRuleDebugResponse(
        expectData(
          await api.POST('/api/v1/rules/dry-run', {
            params: { header },
            body: input.request,
            bodySerializer: () => exactRuleDebugBody(input),
            parseAs: 'text'
          })
        )
      ) as RuleDryRunResult
  });
}

/** 解释字段来源与候选决策；只接受请求体中的合成输入。 */
export function useRuleExplain(): UseMutationResult<RuleExplainResult, unknown, RuleDebugInput> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (input: RuleDebugInput) =>
      parseExactRuleDebugResponse(
        expectData(
          await api.POST('/api/v1/rules/explain', {
            params: { header },
            body: input.request,
            bodySerializer: () => exactRuleDebugBody(input),
            parseAs: 'text'
          })
        )
      ) as RuleExplainResult
  });
}

/** 返回受限、脱敏的规则求值 Trace。 */
export function useRuleTrace(): UseMutationResult<RuleTraceResult, unknown, RuleDebugInput> {
  const header = useCsrfHeaders();
  return useMutation({
    mutationFn: async (input: RuleDebugInput) =>
      parseExactRuleDebugResponse(
        expectData(
          await api.POST('/api/v1/rules/trace', {
            params: { header },
            body: input.request,
            bodySerializer: () => exactRuleDebugBody(input),
            parseAs: 'text'
          })
        )
      ) as RuleTraceResult
  });
}

/** 导出某个 RuleVersion 的规范 JSON。影响评估的 `before` 从这里取。 */
export function useExportRuleVersion(semanticHash: string | null) {
  return useQuery({
    queryKey: ['rules', 'export', semanticHash],
    enabled: semanticHash !== null,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/rule-versions/{semanticHash}/export', {
          params: { path: { semanticHash: semanticHash ?? '' }, query: { format: 'json' } },
          signal,
          parseAs: 'text'
        })
      )
  });
}

/**
 * 规则包 Schema 的可达性探测。
 *
 * 返回 `application/schema+json`，既供规则页独立探测，也由规则包详情与构建期预编译版本比对后
 * 生成 Schema 表单；这个 hook 只读取权威文档，不在客户端改写字段词表。
 */
export function useRuleSchema(enabled: boolean) {
  return useQuery({
    queryKey: ['rules', 'schema'],
    enabled,
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/rules/schema', { signal }))
  });
}

/** 内置、脱敏且随二进制分发的规则模板；表单只把它们载入本地草稿，不自动保存。 */
export function useRuleExamples() {
  return useQuery({
    queryKey: ['rules', 'examples'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/rules/examples', { signal }))
  });
}

export function useSourceRuleBindings(sourceId: string | null) {
  return useQuery({
    queryKey: ['rules', 'bindings', sourceId],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/source-rule-bindings', {
          params: { query: sourceId === null ? {} : { sourceId } },
          signal
        })
      )
  });
}

/** 某个 Source 当前生效的唯一绑定。未匹配或冲突时服务端返回稳定错误，不返回「随便一条」。 */
export function useEffectiveRuleBinding(sourceId: string | null) {
  return useQuery({
    queryKey: ['rules', 'effective-binding', sourceId],
    enabled: sourceId !== null,
    retry: false,
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/sources/{sourceId}/effective-rule-binding', {
          params: { path: { sourceId: sourceId ?? '' } },
          signal
        })
      )
  });
}

export interface CreateDirectBindingInput {
  sourceId: string;
  semanticHash: string;
  priority: number;
  /** 规范 JSON 对象文本，数字不得经过 JavaScript Number。 */
  parameters: string;
  parameterId?: never;
  override?: never;
}

export interface CreateParameterSetBindingInput {
  sourceId: string;
  parameterId: string;
  priority: number;
  /** 一层 canonical object override；数字不得经过 JavaScript Number。 */
  override: string;
  semanticHash?: never;
  parameters?: never;
}

export type CreateBindingInput = CreateDirectBindingInput | CreateParameterSetBindingInput;

export function useCreateSourceRuleBinding(): UseMutationResult<
  SourceRuleBinding,
  unknown,
  CreateBindingInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: CreateBindingInput) =>
      expectData(await api.POST('/api/v1/source-rule-bindings', { params: { header }, body: input })),
    onSuccess: () => {
      invalidate(['rules', 'sources']);
    }
  });
}

export interface UpdateSourceRuleBindingInput {
  bindingId: string;
  status: NonNullable<SourceRuleBinding['status']>;
}

/** 暂停、恢复或显式标记一条 SourceRuleBinding；最终授权仍按该 Binding 的 Source scope 判定。 */
export function useUpdateSourceRuleBinding(): UseMutationResult<
  SourceRuleBinding,
  unknown,
  UpdateSourceRuleBindingInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: UpdateSourceRuleBindingInput) =>
      expectData(
        await api.PATCH('/api/v1/source-rule-bindings/{bindingId}', {
          params: { header, path: { bindingId: input.bindingId } },
          body: { status: input.status }
        })
      ),
    onSuccess: () => {
      invalidate(['rules']);
    }
  });
}

/* ————————————————————————————— 治理 ————————————————————————————— */

export interface BindingIssueFilters {
  sourceId?: string;
  entityType?: BindingIssue['entityType'];
  status?: BindingIssue['status'];
}

/**
 * 绑定问题列表。
 *
 * keyset 游标：只能逐页向后追加，没有租约、没有 CURSOR_EXPIRED 契约，也**不是**作品查询的
 * 签名游标。追加得到的顺序就是服务端顺序，客户端不重排。
 */
export function useBindingIssues(filters: BindingIssueFilters) {
  return useInfiniteQuery({
    queryKey: ['governance', 'binding-issues', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) =>
      expectData(
        await api.GET('/api/v1/binding-issues', {
          params: {
            query: {
              ...filters,
              ...(pageParam === undefined ? {} : { cursor: pageParam }),
              limit: GOVERNANCE_PAGE_SIZE
            }
          },
          signal
        })
      ),
    getNextPageParam: (lastPage) => lastPage.nextCursor
  });
}

export interface ResolveIssueInput {
  issueId: string;
  decision: Schemas['BindingIssueResolveRequest']['decision'];
  targetId?: string;
  version: number;
}

export function useResolveBindingIssue(): UseMutationResult<BindingIssue, unknown, ResolveIssueInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: ResolveIssueInput) =>
      expectData(
        await api.POST('/api/v1/binding-issues/{issueId}/resolve', {
          params: { header, path: { issueId: input.issueId } },
          body: {
            decision: input.decision,
            version: input.version,
            ...(input.targetId === undefined ? {} : { targetId: input.targetId })
          }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export interface IssueVersionInput {
  issueId: string;
  version: number;
}

export function useDismissBindingIssue(): UseMutationResult<BindingIssue, unknown, IssueVersionInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: IssueVersionInput) =>
      expectData(
        await api.POST('/api/v1/binding-issues/{issueId}/dismiss', {
          params: { header, path: { issueId: input.issueId } },
          body: { version: input.version }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export function useReopenBindingIssue(): UseMutationResult<BindingIssue, unknown, IssueVersionInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: IssueVersionInput) =>
      expectData(
        await api.POST('/api/v1/binding-issues/{issueId}/reopen', {
          params: { header, path: { issueId: input.issueId } },
          body: { version: input.version }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export interface ResolveStructureInput {
  issueId: string;
  action: Schemas['SourceStructureDecisionRequest']['action'];
  targetSourceKey?: string;
  targetWorkId?: string;
  version: number;
}

export function useResolveStructureIssue(): UseMutationResult<
  SourceStructureDecision,
  unknown,
  ResolveStructureInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: ResolveStructureInput) =>
      expectData(
        await api.POST('/api/v1/binding-issues/{issueId}/resolve-structure', {
          params: { header, path: { issueId: input.issueId } },
          body: {
            action: input.action,
            version: input.version,
            ...(input.targetSourceKey === undefined ? {} : { targetSourceKey: input.targetSourceKey }),
            ...(input.targetWorkId === undefined ? {} : { targetWorkId: input.targetWorkId })
          }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export function useStructureDecisions(sourceId: string | null) {
  return useQuery({
    queryKey: ['governance', 'structure-decisions', sourceId],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/source-structure-decisions', {
          params: {
            query:
              sourceId === null ? { limit: GOVERNANCE_PAGE_SIZE } : { sourceId, limit: GOVERNANCE_PAGE_SIZE }
          },
          signal
        })
      )
  });
}

export interface UndoDecisionInput {
  decisionId: string;
  version: number;
}

/**
 * 撤回结构决策。
 *
 * 只适用于尚未被扫描消费的 pre-seed Binding；已被消费后服务端返回结构化 CONFLICT，
 * 且**不会**执行已生效结构变化的反向操作。
 */
export function useUndoStructureDecision(): UseMutationResult<
  SourceStructureDecision,
  unknown,
  UndoDecisionInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: UndoDecisionInput) =>
      expectData(
        await api.POST('/api/v1/source-structure-decisions/{decisionId}/undo', {
          params: { header, path: { decisionId: input.decisionId } },
          body: { version: input.version }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export interface OrphanFilters {
  sourceId?: string;
  entityType?: OrphanCandidate['entityType'];
}

export function useOrphanCandidates(filters: OrphanFilters) {
  return useInfiniteQuery({
    queryKey: ['governance', 'orphan-candidates', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) =>
      expectData(
        await api.GET('/api/v1/orphan-candidates', {
          params: {
            query: {
              ...filters,
              ...(pageParam === undefined ? {} : { cursor: pageParam }),
              limit: GOVERNANCE_PAGE_SIZE
            }
          },
          signal
        })
      ),
    getNextPageParam: (lastPage) => lastPage.nextCursor
  });
}

export interface OrphanDecisionInput {
  bindingId: string;
  decision: Schemas['OrphanDecisionRequest']['decision'];
  extendScans?: number;
}

export function useDecideOrphanCandidate(): UseMutationResult<
  OrphanDecisionResult,
  unknown,
  OrphanDecisionInput
> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: OrphanDecisionInput) =>
      expectData(
        await api.POST('/api/v1/orphan-candidates/{bindingId}/decide', {
          params: { header, path: { bindingId: input.bindingId } },
          body: {
            decision: input.decision,
            ...(input.extendScans === undefined ? {} : { extendScans: input.extendScans })
          }
        })
      ),
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}

export type BindingActionKind = 'unbind-work' | 'unbind-media' | 'undo-work-unbind' | 'undo-media-unbind';

export interface BindingActionInput {
  kind: BindingActionKind;
  sourceId: string;
  sourceKey: string;
}

/** 人工解绑与撤销解绑。三个端点都只接受一个对象，没有批量形态。 */
export function useBindingAction(): UseMutationResult<BindingActionResult, unknown, BindingActionInput> {
  const header = useCsrfHeaders();
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: async (input: BindingActionInput) => {
      const body = { sourceId: input.sourceId, sourceKey: input.sourceKey };
      if (input.kind === 'unbind-work') {
        return expectData(
          await api.POST('/api/v1/binding-actions/unbind-work', { params: { header }, body })
        );
      }
      if (input.kind === 'unbind-media') {
        return expectData(
          await api.POST('/api/v1/binding-actions/unbind-media', { params: { header }, body })
        );
      }
      return expectData(
        await api.POST('/api/v1/binding-actions/undo-unbind', {
          params: { header },
          body: {
            ...body,
            entityKind: input.kind === 'undo-work-unbind' ? 'work' : 'media'
          }
        })
      );
    },
    onSuccess: () => {
      invalidate(['governance', 'bindings']);
    }
  });
}
