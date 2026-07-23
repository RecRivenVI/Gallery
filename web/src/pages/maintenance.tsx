import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from 'react-aria-components';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
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

export function MaintenancePage() {
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const backups = useQuery({
    queryKey: ['backups'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/admin/control-backups', { signal }))
  });
  const action = useMutation({
    mutationFn: async (kind: 'backup' | 'gc' | 'checkpoint' | 'vacuum') => {
      const headers = csrfHeaders(bootstrap.csrfToken);
      if (kind === 'backup')
        return expectData(await api.POST('/api/v1/admin/control-backups', { params: { header: headers } }));
      if (kind === 'gc')
        return expectData(
          await api.POST('/api/v1/admin/maintenance/gc', {
            params: { header: headers },
            body: { retentionSeconds: 86400, dryRun: false }
          })
        );
      if (kind === 'checkpoint')
        return expectData(
          await api.POST('/api/v1/admin/maintenance/checkpoint', { params: { header: headers } })
        );
      return expectData(await api.POST('/api/v1/admin/maintenance/vacuum', { params: { header: headers } }));
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['backups'] });
      await client.invalidateQueries({ queryKey: ['jobs'] });
    }
  });
  const verify = useMutation({
    mutationFn: async (backupId: string) =>
      expectData(
        await api.POST('/api/v1/admin/control-restores/verify', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: { backupId }
        })
      )
  });
  if (backups.isPending) return <LoadingState />;
  if (backups.isError) return <ErrorState error={backups.error} />;
  return (
    <>
      <PageHeader
        title="备份与维护"
        description="control.db 最高备份优先；维护操作是可恢复的持久 Job，恢复必须先 Dry Run。"
        actions={
          can('admin.backup') && (
            <Button className="button primary" onPress={() => action.mutate('backup')}>
              创建 control 备份
            </Button>
          )
        }
      />
      <section className="panel">
        <h2>维护 Job</h2>
        <div className="button-row">
          {can('admin.maintenance') && (
            <>
              <ConfirmAction
                label="运行 GC"
                title="运行安全 GC？"
                detail="服务端会先做空间与活跃引用检查。"
                onConfirm={async () => {
                  await action.mutateAsync('gc');
                }}
              />
              <Button className="button secondary" onPress={() => action.mutate('checkpoint')}>
                WAL checkpoint
              </Button>
              <ConfirmAction
                label="运行 VACUUM"
                title="运行 VACUUM？"
                detail="服务端会先做空间预检并以持久 Job 执行。"
                onConfirm={async () => {
                  await action.mutateAsync('vacuum');
                }}
              />
            </>
          )}
        </div>
        {action.isError && (
          <p role="alert" className="field-error">
            {errorMessage(action.error)}
          </p>
        )}
      </section>
      <h2>已发布备份</h2>
      {backups.data.backups.length === 0 ? (
        <EmptyState title="没有备份" detail="创建产品级 control.db 备份后会显示经过校验的 manifest。" />
      ) : (
        <div className="card-grid">
          {backups.data.backups.map((backup) => (
            <article className="card" key={backup.backupId}>
              <h3>{backup.backupId}</h3>
              <StatusBadge tone="success">manifest v{backup.manifestVersion}</StatusBadge>
              <DefinitionList
                items={[
                  ['创建', formatDate(backup.createdAt)],
                  ['大小', backup.database.sizeBytes],
                  ['SHA-256', <code>{backup.database.checksum}</code>]
                ]}
              />
              <Button className="button secondary" onPress={() => verify.mutate(backup.backupId)}>
                恢复 Dry Run
              </Button>
            </article>
          ))}
        </div>
      )}
      {verify.data && (
        <section className="callout">
          <h2>Dry Run 结果</h2>
          <pre className="json-output">{JSON.stringify(verify.data, null, 2)}</pre>
          <p>本页不会自动登记恢复；实际恢复需要额外的破坏性确认与重启窗口。</p>
        </section>
      )}
    </>
  );
}
