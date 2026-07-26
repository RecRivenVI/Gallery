/*
 * 协议枚举 → 中文标签与语义色。
 *
 * 集中一份的理由：同一个 `needs_repair` 在任务列表、任务详情和概览里必须是同一个词、同一种
 * 色调。分散翻译会让用户以为它们是不同的状态。
 *
 * 这里的顺序常量只用于**派生汇总**（例如概览的状态计数）的稳定展示顺序，不用于重排服务端
 * 返回的列表——列表顺序永远是服务端的。
 */

import type { Tone } from '../design';
import type { BindingIssue, Job, OrphanCandidate, RuleImpactResult, ScanProfile } from './api';

type JobStatus = Job['status'];
type JobType = Job['type'];

export const JOB_STATUS_LABELS: Record<JobStatus, string> = {
  queued: '排队中',
  running: '执行中',
  publishing: '发布中',
  cancelling: '取消中',
  completed: '已完成',
  failed: '已失败',
  cancelled: '已取消',
  superseded: '已被取代',
  needs_repair: '需要修复'
};

export const JOB_STATUS_TONES: Record<JobStatus, Tone> = {
  queued: 'neutral',
  running: 'accent',
  publishing: 'accent',
  cancelling: 'warning',
  completed: 'success',
  failed: 'danger',
  cancelled: 'neutral',
  superseded: 'neutral',
  needs_repair: 'danger'
};

/** 概览计数的展示顺序：先看进行中，再看失败，最后才是历史终态。 */
export const JOB_STATUS_ORDER: readonly JobStatus[] = [
  'running',
  'publishing',
  'queued',
  'cancelling',
  'needs_repair',
  'failed',
  'completed',
  'cancelled',
  'superseded'
];

export const JOB_TYPE_LABELS: Record<JobType, string> = {
  scan: '扫描',
  hash: '内容哈希',
  overlay_projection: 'Overlay 投影',
  derived: '派生资源',
  external_tool: '外部工具',
  control_backup: 'control 备份',
  control_restore: 'control 恢复',
  catalog_gc: 'Catalog GC',
  catalog_checkpoint: 'Catalog checkpoint',
  catalog_vacuum: 'Catalog vacuum',
  derived_gc: '派生资源 GC'
};

/**
 * 任务变更所需的 capability，按服务端 `authorizeJob` 的派生规则。
 *
 * 绑定了 Source 的任务一律用 `scan.run`（按 Source 作用域），与类型无关；只有不绑定 Source
 * 的任务才落到下表。词表里没有 jobs.cancel / jobs.retry。
 */
export const JOB_MUTATION_CAPABILITY: Record<JobType, string> = {
  scan: 'scan.run',
  hash: 'scan.run',
  external_tool: 'scan.run',
  overlay_projection: 'overlays.write',
  derived: 'media.derive',
  control_backup: 'admin.backup',
  control_restore: 'admin.restore',
  catalog_gc: 'admin.maintenance',
  catalog_checkpoint: 'admin.maintenance',
  catalog_vacuum: 'admin.maintenance',
  derived_gc: 'admin.maintenance'
};

export const SCAN_PROFILE_LABELS: Record<ScanProfile, string> = {
  index: 'index（仅首次扫描）',
  incremental: 'incremental（默认）',
  verify: 'verify（强制重新哈希）'
};

export const SCAN_PROFILE_DESCRIPTIONS: Record<ScanProfile, string> = {
  index:
    '不计算内容摘要，媒体以 located_unverified 快速发布。只对从未发布、也没有持久历史的 Source 有效；否则服务端稳定拒绝为 409。',
  incremental: '按既往观察复用未变化媒体的已确认摘要，只对新增或疑似变化的媒体计算摘要。',
  verify: '忽略既往观察，对本次扫描到的媒体强制重新计算完整摘要。耗时最长。'
};

export const SOURCE_SCAN_STATUS_LABELS: Record<string, string> = {
  unknown: '未知',
  online: '在线',
  offline: '离线',
  degraded: '降级',
  permission_denied: '无系统权限',
  identity_changed: '身份已变化'
};

export const SOURCE_SCAN_STATUS_TONES: Record<string, Tone> = {
  unknown: 'neutral',
  online: 'success',
  offline: 'warning',
  degraded: 'warning',
  permission_denied: 'danger',
  identity_changed: 'danger'
};

export const BINDING_ISSUE_STATUS_LABELS: Record<BindingIssue['status'], string> = {
  open: '待处理',
  resolved: '已修复',
  dismissed: '已忽略',
  superseded: '已被取代',
  stale: '已过期'
};

export const BINDING_ISSUE_STATUS_TONES: Record<BindingIssue['status'], Tone> = {
  open: 'warning',
  resolved: 'success',
  dismissed: 'neutral',
  superseded: 'neutral',
  stale: 'neutral'
};

export const ENTITY_TYPE_LABELS: Record<OrphanCandidate['entityType'], string> = {
  work: '作品',
  creator: '创作者',
  media: '媒体'
};

export const IMPACT_CATEGORY_LABELS: Record<RuleImpactResult['category'], string> = {
  NO_ACTION: '无需动作',
  REPROJECT: '需要重投影',
  RESCAN_PARTIAL: '需要部分重扫',
  RESCAN_FULL: '需要完整重扫',
  BINDING_REVIEW: '需要人工复核绑定',
  INVALID: '变更不合法'
};

export const IMPACT_CATEGORY_TONES: Record<RuleImpactResult['category'], Tone> = {
  NO_ACTION: 'success',
  REPROJECT: 'accent',
  RESCAN_PARTIAL: 'warning',
  RESCAN_FULL: 'warning',
  BINDING_REVIEW: 'warning',
  INVALID: 'danger'
};

export const STRUCTURE_ACTION_LABELS: Record<string, string> = {
  split_inherit: '拆分：继承原作品',
  split_keep_same: '拆分：保持同一作品',
  split_create_new: '拆分：创建新作品',
  merge_bind_existing: '合并：绑定到已有作品',
  merge_create_new: '合并：创建新作品'
};

export const ORPHAN_DECISION_LABELS: Record<string, string> = {
  retain: '保留（重置缺席计数）',
  extend: '延长观察窗口',
  confirm_orphaned: '确认为孤儿',
  unbind: '人工解绑'
};
