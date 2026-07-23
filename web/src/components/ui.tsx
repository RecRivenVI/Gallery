import { Button, Dialog, DialogTrigger, Heading, Modal, ModalOverlay } from 'react-aria-components';
import { Link } from 'react-router-dom';
import { errorMessage } from '../api/client';
import type { ReactNode } from 'react';

export function PageHeader({
  title,
  description,
  actions
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}

export function StatusBadge({
  children,
  tone = 'neutral'
}: {
  children: ReactNode;
  tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info';
}) {
  return <span className={`status-badge status-${tone}`}>{children}</span>;
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <section className="state-panel state-error" role="alert">
      <h2>无法完成请求</h2>
      <p>{errorMessage(error)}</p>
      {onRetry && (
        <Button className="button secondary" onPress={onRetry}>
          重试
        </Button>
      )}
    </section>
  );
}

export function EmptyState({ title, detail, action }: { title: string; detail: string; action?: ReactNode }) {
  return (
    <section className="state-panel">
      <h2>{title}</h2>
      <p>{detail}</p>
      {action}
    </section>
  );
}

export function LoadingState({ label = '正在加载…' }: { label?: string }) {
  return (
    <div className="state-panel" role="status">
      <span className="spinner" aria-hidden="true" />
      {label}
    </div>
  );
}

export function DefinitionList({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="definition-list">
      {items.map(([term, value]) => (
        <div key={term}>
          <dt>{term}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function ConfirmAction({
  label,
  title,
  detail,
  danger,
  onConfirm
}: {
  label: string;
  title: string;
  detail: string;
  danger?: boolean;
  onConfirm: () => void | Promise<void>;
}) {
  return (
    <DialogTrigger>
      <Button className={`button ${danger ? 'danger' : 'secondary'}`}>{label}</Button>
      <ModalOverlay className="modal-overlay" isDismissable>
        <Modal className="modal">
          <Dialog className="dialog">
            {({ close }) => (
              <>
                <Heading slot="title">{title}</Heading>
                <p>{detail}</p>
                <div className="dialog-actions">
                  <Button className="button ghost" onPress={close}>
                    取消
                  </Button>
                  <Button
                    className={`button ${danger ? 'danger' : 'primary'}`}
                    onPress={() => {
                      void Promise.resolve(onConfirm()).then(close);
                    }}
                  >
                    确认
                  </Button>
                </div>
              </>
            )}
          </Dialog>
        </Modal>
      </ModalOverlay>
    </DialogTrigger>
  );
}

export function IdLink({ to, id }: { to: string; id: string }) {
  return (
    <Link className="mono-link" to={to} title={id}>
      {shortId(id)}
    </Link>
  );
}

export function shortId(value: string): string {
  const index = value.indexOf('_');
  if (index === -1 || value.length <= index + 13) return value;
  return `${value.slice(0, index + 1)}${value.slice(index + 1, index + 9)}…`;
}

export function formatDate(value?: string | null): string {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value)
  );
}

export function ProgressBar({ value, label }: { value: number; label: string }) {
  const normalized = Math.max(0, Math.min(1, value));
  return (
    <div
      className="progress"
      aria-label={label}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(normalized * 100)}
    >
      <span style={{ width: `${normalized * 100}%` }} />
    </div>
  );
}
