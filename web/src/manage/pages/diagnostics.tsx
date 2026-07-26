/*
 * 验证和诊断：维护任务、control 备份、恢复验证，以及服务端诊断能力的真实边界。
 *
 * 这一页最重要的两句话都是「服务端做不到什么」：
 *   - 备份只有清单，没有字节，既不能下载也不能上传；
 *   - 恢复只是登记，下次启动才生效，而且没有任何事件会告诉你服务是否重启过。
 * 把它们写清楚，比多做一个按钮重要得多。
 */

import { useState } from 'react';
import { Badge, Button, Checkbox, Select, TextInput, useToast } from '../../design';
import { useCapability } from '../../shared/session';
import {
  useControlBackups,
  useCreateControlBackup,
  useCurrentPublication,
  useHealth,
  useRequestControlRestore,
  useRunMaintenance,
  useVerifyControlRestore,
  type MaintenanceKind
} from '../api';
import {
  Absent,
  AsyncPanel,
  BoolBadge,
  ConfirmAction,
  ContractNoteList,
  DataTable,
  Facts,
  InlineError,
  MonoId,
  PageHeader,
  Section,
  formatBytes,
  formatDateTime
} from '../ui';

const MAINTENANCE_LABELS: Record<MaintenanceKind, string> = {
  gc: 'Catalog GC（回收过期 revision 与暂存）',
  checkpoint: 'WAL checkpoint',
  vacuum: 'VACUUM（重建数据库文件）'
};

const MAINTENANCE_DESCRIPTIONS: Record<MaintenanceKind, string> = {
  gc: '回收超过保留期的 Catalog revision 与暂存数据。GC 不会删除仍在被读取的活跃资源；空间不足时服务端会在写入前中止。',
  checkpoint: '把 WAL 内容合并回主数据库文件。不改变任何逻辑数据。',
  vacuum: '重建数据库文件以回收碎片空间。需要与数据库同等量级的临时空间，服务端会先做空间预检。'
};

const DEFAULT_RETENTION_SECONDS = 86_400;

function MaintenancePanel() {
  const { show } = useToast();
  const canMaintain = useCapability('admin.maintenance');
  const run = useRunMaintenance();
  const [kind, setKind] = useState<MaintenanceKind>('gc');
  const [retention, setRetention] = useState<string>(String(DEFAULT_RETENTION_SECONDS));
  const [dryRun, setDryRun] = useState(true);

  const retentionSeconds = Number.parseInt(retention, 10);
  const retentionValid = Number.isFinite(retentionSeconds) && retentionSeconds >= 0;

  return (
    <Section
      title="维护任务"
      description="三个维护端点都创建持久 Job 并返回服务端的空间预检结论。它们不是同步操作，进度在「扫描与任务」里跟踪。"
    >
      {canMaintain ? null : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 admin.maintenance，维护入口已隐藏。capability 不是最终授权判断，
          若你确信应当有权，请让管理员检查授权后重新登录。
        </p>
      )}
      {canMaintain ? (
        <div className="manage-form">
          <div className="manage-form__row">
            <Select
              label="维护类型"
              options={(Object.keys(MAINTENANCE_LABELS) as MaintenanceKind[]).map((value) => ({
                id: value,
                label: MAINTENANCE_LABELS[value]
              }))}
              selectedKey={kind}
              onSelectionChange={(key) => {
                if (key !== null) setKind(key as MaintenanceKind);
              }}
            />
            {kind === 'gc' ? (
              <TextInput
                label="保留期（秒）"
                value={retention}
                onChange={setRetention}
                description="早于该保留期的 revision 才会被回收。"
                errorMessage={retentionValid ? undefined : '必须是非负整数'}
              />
            ) : null}
          </div>
          {kind === 'gc' ? (
            <Checkbox isSelected={dryRun} onChange={setDryRun}>
              干跑：只估算将要回收的内容，不实际删除
            </Checkbox>
          ) : null}
          <p className="manage-section__description">{MAINTENANCE_DESCRIPTIONS[kind]}</p>
          <div className="manage-form__actions">
            <ConfirmAction
              label="创建维护任务"
              dialogTitle="创建维护任务"
              confirmLabel="确认创建"
              variant={kind === 'gc' && dryRun ? 'primary' : 'danger'}
              isDisabled={kind === 'gc' && !retentionValid}
              isPending={run.isPending}
              description={
                kind === 'gc' && dryRun
                  ? '干跑不会删除任何数据，只返回估算结果。'
                  : '该操作会修改 catalog.db。执行期间相关读取可能被拒绝为 MAINTENANCE_BLOCKED，请在没有扫描或发布进行时执行。'
              }
              onConfirm={() => {
                run.mutate(
                  {
                    kind,
                    retentionSeconds: retentionValid ? retentionSeconds : DEFAULT_RETENTION_SECONDS,
                    dryRun
                  },
                  {
                    onSuccess: (result) => {
                      show({
                        title: '维护任务已排队',
                        description: `任务 ${result.job.id}`,
                        tone: 'success'
                      });
                    }
                  }
                );
              }}
            />
          </div>
          <InlineError error={run.error} title="维护任务未能创建" />
          {run.data === undefined ? null : (
            <Facts
              items={[
                { term: '任务 ID', value: <MonoId value={run.data.job.id} label="任务 ID" /> },
                { term: '预检操作', value: run.data.spaceEstimate.operation },
                { term: '需要空间', value: formatBytes(run.data.spaceEstimate.requiredBytes) },
                { term: '可用空间', value: formatBytes(run.data.spaceEstimate.availableBytes) },
                {
                  term: '空间是否充足',
                  value: (
                    <BoolBadge
                      value={run.data.spaceEstimate.sufficient}
                      trueLabel="充足"
                      falseLabel="不足"
                      falseTone="danger"
                    />
                  )
                },
                {
                  term: '估算口径',
                  value: run.data.spaceEstimate.conservative ? '保守估算（偏大）' : '常规估算'
                }
              ]}
            />
          )}
        </div>
      ) : null}
    </Section>
  );
}

function BackupPanel() {
  const { show } = useToast();
  const canBackup = useCapability('admin.backup');
  const backups = useControlBackups();
  const create = useCreateControlBackup();

  return (
    <Section
      title="control 备份"
      description="control.db 保存不可重建的用户事实，是最高备份优先级。备份是异步任务，完成后出现在下表。"
      actions={
        canBackup ? (
          <Button
            variant="primary"
            isPending={create.isPending}
            onPress={() => {
              create.mutate(undefined, {
                onSuccess: (job) => {
                  show({ title: '备份任务已排队', description: `任务 ${job.id}`, tone: 'success' });
                }
              });
            }}
          >
            创建备份
          </Button>
        ) : undefined
      }
    >
      <ContractNoteList area="maintenance" only={['maintenance-backup-manifest-only']} />
      <InlineError error={create.error} title="备份任务未能创建" />
      <AsyncPanel query={backups}>
        {(data) => (
          <DataTable
            caption="control 备份清单"
            rows={data.backups}
            rowKey={(row) => row.backupId}
            emptyTitle="还没有任何备份"
            emptyDescription="创建一次备份后，它的清单会出现在这里。备份文件只存在于服务端的应用数据目录中。"
            columns={[
              {
                id: 'id',
                header: '备份 ID',
                render: (row) => <MonoId value={row.backupId} label="备份 ID" />
              },
              { id: 'createdAt', header: '创建时间', render: (row) => formatDateTime(row.createdAt) },
              { id: 'app', header: '应用版本', render: (row) => row.appVersion },
              { id: 'schema', header: 'Schema 版本', render: (row) => row.schemaVersion },
              { id: 'size', header: '数据库大小', render: (row) => formatBytes(row.database.sizeBytes) },
              {
                id: 'checksum',
                header: '校验和',
                render: (row) => <MonoId value={row.database.checksum} label="校验和" />
              },
              { id: 'algo', header: '算法', render: (row) => row.database.checksumAlgorithm },
              { id: 'security', header: '安全材料处理', render: (row) => row.security.note, wrap: true }
            ]}
          />
        )}
      </AsyncPanel>
    </Section>
  );
}

function RestorePanel() {
  const { show } = useToast();
  const canRestore = useCapability('admin.restore');
  const backups = useControlBackups();
  const verify = useVerifyControlRestore();
  const request = useRequestControlRestore();
  const [backupId, setBackupId] = useState<string | null>(null);

  return (
    <Section
      title="恢复"
      description="验证是干跑：它只给出结论，不改变任何东西。登记恢复也不会立刻恢复——真正的恢复发生在 galleryd 下次启动时。"
    >
      <ContractNoteList area="maintenance" only={['maintenance-restore-next-start']} />
      {canRestore ? null : (
        <p className="manage-section__description">
          当前主体在 global scope 没有 admin.restore，恢复入口已隐藏。
        </p>
      )}
      {canRestore ? (
        <AsyncPanel query={backups}>
          {(data) => (
            <div className="manage-form">
              <Select
                label="要恢复的备份"
                placeholder="选择一个备份清单"
                options={data.backups.map((item) => ({
                  id: item.backupId,
                  label: `${item.backupId}（${formatDateTime(item.createdAt)}）`
                }))}
                selectedKey={backupId}
                onSelectionChange={setBackupId}
              />
              <div className="manage-form__actions">
                <Button
                  variant="secondary"
                  isDisabled={backupId === null}
                  isPending={verify.isPending}
                  onPress={() => {
                    if (backupId !== null) verify.mutate(backupId);
                  }}
                >
                  验证（干跑，不改变任何东西）
                </Button>
                <ConfirmAction
                  label="登记恢复"
                  dialogTitle="登记 control 恢复"
                  confirmLabel="确认登记"
                  isDisabled={backupId === null}
                  isPending={request.isPending}
                  description="登记后本次运行不会发生任何变化；恢复在 galleryd 下次启动时执行，届时当前 control.db 会被备份内容替换。没有任何事件会通知你服务是否已经重启，请自行重启并复核结果。"
                  onConfirm={() => {
                    if (backupId === null) return;
                    request.mutate(backupId, {
                      onSuccess: () => {
                        show({
                          title: '恢复请求已登记',
                          description: '下次启动 galleryd 时才会应用。请手动重启并复核。',
                          tone: 'warning',
                          timeoutMs: 0
                        });
                      }
                    });
                  }}
                />
              </div>
              <InlineError error={verify.error} title="验证未能完成" />
              <InlineError error={request.error} title="恢复请求未能登记" />
              {verify.data === undefined ? null : <RestoreReport report={verify.data} title="干跑验证结论" />}
              {request.data === undefined ? null : (
                <>
                  <p className="manage-section__description">
                    <Badge tone="warning">
                      {request.data.restartRequired ? '已登记，需要重启 galleryd 才会生效' : '已登记'}
                    </Badge>
                  </p>
                  <RestoreReport report={request.data.report} title="登记时的验证结论" />
                </>
              )}
            </div>
          )}
        </AsyncPanel>
      ) : null}
    </Section>
  );
}

function RestoreReport({
  report,
  title
}: {
  report: {
    backupId: string;
    compatible: boolean;
    backupSchemaVersion: number;
    currentSchemaVersion: number;
    willMigrate: boolean;
    checksumVerified: boolean;
    integrityOk: boolean;
    invariantsOk: boolean;
    detail?: string;
  };
  title: string;
}) {
  return (
    <>
      <h3 className="manage-section__title">{title}</h3>
      <Facts
        items={[
          { term: '备份 ID', value: <MonoId value={report.backupId} label="备份 ID" /> },
          {
            term: '兼容性',
            value: (
              <BoolBadge value={report.compatible} trueLabel="兼容" falseLabel="不兼容" falseTone="danger" />
            )
          },
          { term: '备份 Schema 版本', value: report.backupSchemaVersion },
          { term: '当前 Schema 版本', value: report.currentSchemaVersion },
          {
            term: '恢复后是否需要迁移',
            value: (
              <BoolBadge
                value={report.willMigrate}
                trueLabel="需要迁移"
                falseLabel="无需迁移"
                trueTone="warning"
                falseTone="success"
              />
            )
          },
          {
            term: '校验和',
            value: (
              <BoolBadge
                value={report.checksumVerified}
                trueLabel="已核对"
                falseLabel="未通过"
                falseTone="danger"
              />
            )
          },
          {
            term: '完整性',
            value: (
              <BoolBadge value={report.integrityOk} trueLabel="通过" falseLabel="未通过" falseTone="danger" />
            )
          },
          {
            term: '不变量',
            value: (
              <BoolBadge
                value={report.invariantsOk}
                trueLabel="通过"
                falseLabel="未通过"
                falseTone="danger"
              />
            )
          },
          { term: '说明', value: report.detail ?? <Absent /> }
        ]}
      />
    </>
  );
}

function DiagnosticsCapabilityPanel() {
  const health = useHealth();
  const publication = useCurrentPublication();
  return (
    <Section
      title="可用的诊断信息"
      description="下面是服务端确实提供的全部诊断面。除此之外没有日志、指标或追踪接口，报告问题时请引用出错时界面上显示的稳定 code 与关联 ID。"
    >
      <AsyncPanel query={health}>
        {(data) => (
          <Facts
            items={[
              { term: 'control.db', value: <Badge tone="success">{data.databases.control}</Badge> },
              { term: 'catalog.db', value: <Badge tone="success">{data.databases.catalog}</Badge> }
            ]}
          />
        )}
      </AsyncPanel>
      <AsyncPanel query={publication}>
        {(data) => (
          <Facts
            items={[
              { term: '当前 queryPublicationId', value: <MonoId value={data.id} label="快照 ID" /> },
              { term: '发布时间', value: formatDateTime(data.createdAt) }
            ]}
          />
        )}
      </AsyncPanel>
      <ContractNoteList area="diagnostics" />
    </Section>
  );
}

export function DiagnosticsPage() {
  return (
    <>
      <PageHeader
        title="验证和诊断"
        lead="维护、备份与恢复验证都在这里。恢复只是登记，重启后才应用；备份只有清单，没有可下载的字节。这两点是服务端的当前事实，不是本界面尚未实现的功能。"
      />
      <MaintenancePanel />
      <BackupPanel />
      <RestorePanel />
      <DiagnosticsCapabilityPanel />
    </>
  );
}
