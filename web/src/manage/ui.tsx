/*
 * 管理端复用组件。
 *
 * 这里**不是**第二套设计系统：外观一律来自 `../design`，本文件只把「管理端反复出现的信息
 * 结构」固化下来——表格、事实清单、稳定 code 与关联 ID 的常驻展示、查询五态、危险操作的二次
 * 确认、只出现一次的密文，以及契约限制说明。
 *
 * 三条必须遵守的约定：
 *
 * 1. **不本地重排服务端列表。** DataTable 按传入顺序渲染，不提供任何排序控件。排序、过滤、
 *    分页语义属于服务端。
 * 2. **失败不许静默。** 查询失败用 ErrorState，变更失败用页面内的 InlineError；两者都常驻
 *    稳定 code 与关联 ID。FORBIDDEN 与 NOT_FOUND 不得呈现成「空」。
 * 3. **危险操作必经 Dialog。** 吊销、停用、解绑、维护、恢复一律二次确认。
 */

import { useCallback } from 'react';
import type { ReactNode } from 'react';
import {
  Badge,
  Button,
  Dialog,
  EmptyState,
  ErrorState,
  IconButton,
  Spinner,
  VisuallyHidden,
  useToast,
  type ButtonVariant,
  type Tone
} from '../design';
import { describeError, errorCode, errorCorrelationId } from '../shared/errors';
import { contractNotes, type ContractArea, type ContractNote } from './contract';
import './manage.css';

/* ————————————————————————————— 页面骨架 ————————————————————————————— */

export function PageHeader({ title, lead }: { title: string; lead: ReactNode }) {
  return (
    <header>
      <h1 className="manage-page__title">{title}</h1>
      <p className="manage-page__lead">{lead}</p>
    </header>
  );
}

export interface SectionProps {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}

export function Section({ title, description, actions, children }: SectionProps) {
  return (
    <section className="manage-section">
      <div className="manage-section__header">
        <h2 className="manage-section__title">{title}</h2>
        {actions ? <div className="manage-section__actions">{actions}</div> : null}
      </div>
      {description ? <p className="manage-section__description">{description}</p> : null}
      {children}
    </section>
  );
}

/* ————————————————————————————— 表格 ————————————————————————————— */

export interface Column<T> {
  id: string;
  header: ReactNode;
  render: (row: T) => ReactNode;
  /** 允许换行的长文本列（说明、路径片段）。默认所有列都不换行以保持行高一致。 */
  wrap?: boolean;
}

export interface DataTableProps<T> {
  /** 表格的无障碍名称。管理端表格很多，屏幕阅读器需要区分它们。 */
  caption: string;
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  emptyTitle: string;
  emptyDescription?: ReactNode;
}

/**
 * 管理端表格。
 *
 * 刻意不提供列排序：服务端拥有排序语义，客户端重排会让游标分页失去一致性。需要不同顺序时
 * 应当由服务端查询参数表达。
 */
export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  emptyTitle,
  emptyDescription
}: DataTableProps<T>) {
  if (rows.length === 0) {
    return <EmptyState title={emptyTitle} description={emptyDescription} />;
  }
  return (
    <div className="manage-table-scroll">
      <table className="manage-table">
        <caption>
          <VisuallyHidden>{caption}</VisuallyHidden>
        </caption>
        <thead>
          <tr>
            {columns.map((column) => (
              <th key={column.id} scope="col">
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((column) => (
                <td
                  key={column.id}
                  className={column.wrap === true ? 'is-wrap' : undefined}
                  data-label={typeof column.header === 'string' ? column.header : undefined}
                >
                  {column.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/* ————————————————————————————— 事实清单 ————————————————————————————— */

export interface Fact {
  term: string;
  value: ReactNode;
}

/** 键值事实清单。管理端用它常驻协议版本、快照 ID、状态等诊断信息。 */
export function Facts({ items }: { items: readonly Fact[] }) {
  return (
    <dl className="manage-facts">
      {items.map((item) => (
        <div className="manage-facts__item" key={item.term}>
          <dt className="manage-facts__term">{item.term}</dt>
          <dd className="manage-facts__value">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * 稳定标识的常驻展示。管理端的诊断价值来自这些 ID 能被原样复制并粘进问题报告里，
 * 因此它们用等宽字体，并且总是带一个复制按钮。
 */
export function MonoId({ value, label }: { value: string; label: string }) {
  const { show } = useToast();
  const copy = useCallback(() => {
    void (async () => {
      try {
        await navigator.clipboard.writeText(value);
        show({ title: `已复制${label}`, tone: 'success', timeoutMs: 3000 });
      } catch {
        show({
          title: `无法复制${label}`,
          description: '浏览器拒绝了剪贴板访问，请手动选择文本。',
          tone: 'warning'
        });
      }
    })();
  }, [value, label, show]);

  return (
    <span className="manage-id">
      <span className="manage-id__value">{value}</span>
      <IconButton label={`复制${label}`} variant="ghost" onPress={copy}>
        ⧉
      </IconButton>
    </span>
  );
}

/**
 * 没有值时的统一占位。避免用空字符串或 0 冒充「没有」。
 *
 * 刻意不加 `aria-label`：aria-label 在无 role 的 span 上是被禁止的属性（axe 的
 * aria-prohibited-attr，属 WCAG 2 A），而可见文字本身已经表达了「没有」。
 */
export function Absent({ children = '—' }: { children?: ReactNode }) {
  return <span className="manage-absent">{children}</span>;
}

/* ————————————————————————————— 查询状态 ————————————————————————————— */

function errorTitle(code: string | undefined): string {
  if (code === 'FORBIDDEN') return '没有执行此操作的权限';
  if (code === 'NOT_FOUND') return '不存在，或当前账户无权查看';
  if (code === 'UNAUTHENTICATED') return '会话已失效';
  return '无法完成请求';
}

/**
 * 查询结果的结构化子集。
 *
 * 刻意不直接用 `UseQueryResult`：治理列表用的是 `useInfiniteQuery`，它的返回类型不同但状态
 * 语义完全一致。用结构子集可以让两种查询共用同一套五态呈现。
 */
export interface AsyncQueryLike<T> {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  data: T | undefined;
  fetchStatus: 'fetching' | 'paused' | 'idle';
  refetch: () => unknown;
}

export interface AsyncPanelProps<T> {
  query: AsyncQueryLike<T>;
  children: (data: T) => ReactNode;
  /** 查询被 enabled:false 停用时显示的内容。 */
  idle?: ReactNode;
  loadingLabel?: string;
}

/**
 * 查询五态。
 *
 * 「无权限」不是「空」：服务端会把部分 FORBIDDEN 伪装成 404，所以 NOT_FOUND 的文案必须同时
 * 覆盖「不存在或无权查看」，绝不能渲染成一个安静的空列表。
 */
export function AsyncPanel<T>({ query, children, idle, loadingLabel = '正在读取快照…' }: AsyncPanelProps<T>) {
  if (query.isPending && query.fetchStatus === 'idle' && idle !== undefined) {
    return <>{idle}</>;
  }
  if (query.isPending) {
    return <Spinner label={loadingLabel} />;
  }
  if (query.isError) {
    const code = errorCode(query.error);
    return (
      <ErrorState
        title={errorTitle(code)}
        description={describeError(query.error)}
        code={code}
        correlationId={errorCorrelationId(query.error)}
        onRetry={() => void query.refetch()}
      />
    );
  }
  // 既非 pending 也非 error 时 data 必然存在，但类型上仍是 T | undefined；
  // 这里用一次显式判断代替类型断言，避免把「缓存被清空」这种真实情况变成运行时崩溃。
  if (query.data === undefined) {
    return <Spinner label={loadingLabel} />;
  }
  return (
    <div className="manage-async-panel">
      {query.fetchStatus === 'fetching' ? (
        <span className="manage-async-panel__refresh" role="status">
          <Spinner decorative />
          正在刷新快照
        </span>
      ) : null}
      {children(query.data)}
    </div>
  );
}

/**
 * 变更失败的页面内呈现。
 *
 * 变更失败不能只弹一条会自动消失的通知：冲突、无权限、校验失败都需要用户读完并决定下一步。
 */
export function InlineError({ error, title = '操作未完成' }: { error: unknown; title?: string }) {
  if (error === null || error === undefined) return null;
  const code = errorCode(error);
  const correlationId = errorCorrelationId(error);
  // FORBIDDEN / NOT_FOUND / UNAUTHENTICATED 有比调用方标题更准确的表述，优先使用它们。
  const specific = code === 'FORBIDDEN' || code === 'NOT_FOUND' || code === 'UNAUTHENTICATED';
  return (
    <div className="manage-inline-error" role="alert">
      <p className="manage-inline-error__title">{specific ? errorTitle(code) : title}</p>
      <p className="manage-inline-error__body">{describeError(error)}</p>
      <p className="manage-inline-error__code">
        {code ?? 'CLIENT_ERROR'}
        {correlationId === undefined ? '' : ` · ${correlationId}`}
      </p>
    </div>
  );
}

/* ————————————————————————————— 危险操作 ————————————————————————————— */

export interface ConfirmActionProps {
  /** 触发按钮上的文字。 */
  label: string;
  dialogTitle: string;
  description: ReactNode;
  confirmLabel: string;
  onConfirm: () => void;
  variant?: ButtonVariant;
  isDisabled?: boolean;
  isPending?: boolean;
}

/** 危险或不可逆操作的二次确认。确认后关闭对话框，失败由调用方在页面内展示。 */
export function ConfirmAction({
  label,
  dialogTitle,
  description,
  confirmLabel,
  onConfirm,
  variant = 'danger',
  isDisabled,
  isPending
}: ConfirmActionProps) {
  return (
    <Dialog
      title={dialogTitle}
      isDismissable={false}
      size="sm"
      trigger={
        <Button variant={variant} isDisabled={isDisabled} isPending={isPending}>
          {label}
        </Button>
      }
      footer={(close) => (
        <>
          <Button variant="ghost" onPress={close}>
            取消
          </Button>
          <Button
            variant={variant}
            onPress={() => {
              onConfirm();
              close();
            }}
          >
            {confirmLabel}
          </Button>
        </>
      )}
    >
      <p className="manage-section__description">{description}</p>
    </Dialog>
  );
}

/* ————————————————————————————— 一次性密文 ————————————————————————————— */

export interface OneTimeSecretProps {
  title: string;
  /** null 表示没有待展示的密文；对话框随之关闭。 */
  secret: string | null;
  /** 关闭后调用方必须把密文置为 null——组件不保留任何副本。 */
  onDismiss: () => void;
  description: ReactNode;
}

/**
 * 只出现一次的密文。
 *
 * 服务端只在创建响应里返回一次 secret，之后只保留前缀。因此这个对话框关闭即丢弃，界面里
 * 没有任何「再看一次」的入口——有的话就是在骗用户。
 */
export function OneTimeSecret({ title, secret, onDismiss, description }: OneTimeSecretProps) {
  const { show } = useToast();
  const copy = useCallback(() => {
    if (secret === null) return;
    void (async () => {
      try {
        await navigator.clipboard.writeText(secret);
        show({ title: '密文已复制到剪贴板', tone: 'success', timeoutMs: 3000 });
      } catch {
        show({ title: '无法访问剪贴板', description: '请手动选择并复制上面的密文。', tone: 'warning' });
      }
    })();
  }, [secret, show]);

  return (
    <Dialog
      title={title}
      isDismissable={false}
      isOpen={secret !== null}
      onOpenChange={(open) => {
        if (!open) onDismiss();
      }}
      footer={
        <>
          <Button variant="secondary" onPress={copy}>
            复制密文
          </Button>
          <Button variant="primary" onPress={onDismiss}>
            我已保存，关闭
          </Button>
        </>
      }
    >
      <p className="manage-section__description">{description}</p>
      <p className="manage-secret" data-testid="one-time-secret">
        {secret ?? ''}
      </p>
      <p className="manage-section__description">
        关闭后本界面会立即丢弃它，没有任何再次查看的入口。没有记录下来就只能吊销后重建。
      </p>
    </Dialog>
  );
}

/* ————————————————————————————— 契约说明 ————————————————————————————— */

export function ContractNoteList({ area, only }: { area: ContractArea; only?: readonly string[] }) {
  const notes = contractNotes(area).filter((note) => only === undefined || only.includes(note.id));
  if (notes.length === 0) return null;
  return (
    <ul className="manage-notes">
      {notes.map((note) => (
        <ContractNoteItem key={note.id} note={note} />
      ))}
    </ul>
  );
}

function ContractNoteItem({ note }: { note: ContractNote }) {
  return (
    <li className={note.gap ? 'manage-note manage-note--gap' : 'manage-note'}>
      <p className="manage-note__title">
        {note.gap ? <Badge tone="warning">服务端缺口</Badge> : <Badge tone="neutral">契约事实</Badge>}
        {note.title}
      </p>
      <p className="manage-note__detail">{note.detail}</p>
    </li>
  );
}

/* ————————————————————————————— 格式化 ————————————————————————————— */

function pad(value: number, width = 2): string {
  return String(value).padStart(width, '0');
}

/**
 * 时间戳格式化。
 *
 * 刻意不用 toLocaleString：它的输出随运行环境的 ICU 数据变化，管理端的诊断信息需要跨机器
 * 可比对。这里固定输出本地时区的 `YYYY-MM-DD HH:mm:ss`。
 */
export function formatDateTime(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    ` ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  );
}

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const;

export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—';
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < BYTE_UNITS.length - 1) {
    size /= 1024;
    unit += 1;
  }
  const suffix = BYTE_UNITS[unit] ?? 'B';
  return `${unit === 0 ? size : size.toFixed(1)} ${suffix}`;
}

/** 布尔事实的可读表达。颜色不是唯一信号，因此永远同时给文字。 */
export function BoolBadge({
  value,
  trueLabel,
  falseLabel,
  trueTone = 'success',
  falseTone = 'warning'
}: {
  value: boolean;
  trueLabel: string;
  falseLabel: string;
  trueTone?: Tone;
  falseTone?: Tone;
}) {
  return <Badge tone={value ? trueTone : falseTone}>{value ? trueLabel : falseLabel}</Badge>;
}
