/*
 * 概览。
 *
 * 概览要回答的问题只有三个：现在正常吗、有没有正在跑的东西、有没有欠账。它**不是**仪表盘：
 * 服务端没有指标接口，任何看起来像趋势图的东西都会是编造的。
 */

import { Link } from 'react-router-dom';
import { Badge } from '../../design';
import { useSession } from '../../shared/session';
import {
  useBindingIssues,
  useCurrentPublication,
  useHealth,
  useJobs,
  useLibraries,
  useOrphanCandidates,
  useSources,
  type Job
} from '../api';
import { JOB_STATUS_LABELS, JOB_STATUS_ORDER, JOB_STATUS_TONES } from '../labels';
import {
  Absent,
  AsyncPanel,
  ContractNoteList,
  Facts,
  MonoId,
  PageHeader,
  Section,
  formatDateTime
} from '../ui';

/** 概览只汇总 newest-first 第一页；完整历史由任务页按需续取。 */
const OVERVIEW_JOB_LIMIT = 100;

function countByStatus(jobs: readonly Job[]): Map<Job['status'], number> {
  const counts = new Map<Job['status'], number>();
  for (const job of jobs) {
    counts.set(job.status, (counts.get(job.status) ?? 0) + 1);
  }
  return counts;
}

export function OverviewPage() {
  const { bootstrap, capabilities } = useSession();
  const health = useHealth();
  const publication = useCurrentPublication();
  const libraries = useLibraries();
  const sources = useSources();
  const jobs = useJobs(null, OVERVIEW_JOB_LIMIT);
  const openIssues = useBindingIssues({ status: 'open' });
  const orphans = useOrphanCandidates({});

  return (
    <>
      <PageHeader
        title="概览"
        lead="这一页只汇报服务端确实提供的事实：两个数据库的健康、当前查询快照、已登记的资料库与来源、最近一段任务，以及待处理的治理欠账。Gallery 不提供指标或日志接口，因此这里没有趋势图。"
      />

      <Section
        title="服务状态"
        description="健康检查只声明 control.db 与 catalog.db 是否可用。它返回 ok 不代表扫描、派生或维护一定可用。"
      >
        <AsyncPanel query={health}>
          {(data) => (
            <Facts
              items={[
                { term: '整体', value: <Badge tone="success">{data.status}</Badge> },
                { term: 'control.db', value: <Badge tone="success">{data.databases.control}</Badge> },
                { term: 'catalog.db', value: <Badge tone="success">{data.databases.catalog}</Badge> },
                { term: 'API 版本', value: data.apiVersion }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>

      <Section
        title="当前查询快照"
        description="外部只能通过服务端签发的 queryPublicationId 选择合法快照。列表与详情必须来自同一个快照，界面不会跨代次拼接。"
      >
        <AsyncPanel query={publication}>
          {(data) => (
            <Facts
              items={[
                { term: 'queryPublicationId', value: <MonoId value={data.id} label="快照 ID" /> },
                {
                  term: 'Catalog revision',
                  value: <MonoId value={data.catalogRevision} label="Catalog revision" />
                },
                {
                  term: 'Overlay 投影 revision',
                  value: <MonoId value={data.overlayProjectionRevision} label="Overlay revision" />
                },
                { term: '发布任务', value: <MonoId value={data.jobId} label="任务 ID" /> },
                { term: 'control 水位', value: data.controlWatermark },
                { term: '发布时间', value: formatDateTime(data.createdAt) }
              ]}
            />
          )}
        </AsyncPanel>
      </Section>

      <div className="manage-grid">
        <Section title="资料库与来源" description="两者都只能创建，不能改名、删除或停用。">
          <AsyncPanel query={libraries}>
            {(data) => <p className="manage-section__description">资料库：{data.libraries.length} 个</p>}
          </AsyncPanel>
          <AsyncPanel query={sources}>
            {(data) => (
              <>
                <p className="manage-section__description">
                  来源：{data.sources.length} 个（离线 {data.sources.filter((item) => !item.available).length}{' '}
                  个）
                </p>
                <p className="manage-section__description">
                  <Link to="/scans">前往扫描与任务</Link>
                </p>
              </>
            )}
          </AsyncPanel>
        </Section>

        <Section
          title="最近任务"
          description={`基于 newest-first 第一页最多 ${OVERVIEW_JOB_LIMIT} 条任务快照的计数；完整历史可在任务页继续载入。`}
        >
          <AsyncPanel query={jobs}>
            {(data) => {
              const counts = countByStatus(data.pages[0]?.jobs ?? []);
              const present = JOB_STATUS_ORDER.filter((status) => (counts.get(status) ?? 0) > 0);
              if (present.length === 0) {
                return <p className="manage-section__description">这一段快照里没有任何任务。</p>;
              }
              return (
                <p className="manage-status-bar">
                  {present.map((status) => (
                    <Badge key={status} tone={JOB_STATUS_TONES[status]}>
                      {JOB_STATUS_LABELS[status]} {counts.get(status) ?? 0}
                    </Badge>
                  ))}
                </p>
              );
            }}
          </AsyncPanel>
          <p className="manage-section__description">
            <Link to="/scans">查看任务列表</Link>
          </p>
        </Section>

        <Section
          title="治理欠账"
          description="治理动作没有批量接口，因此这里的数字同时也是「至少需要多少次独立操作」。"
        >
          <AsyncPanel query={openIssues}>
            {(data) => {
              const loaded = data.pages.reduce((sum, page) => sum + page.issues.length, 0);
              const more = data.pages[data.pages.length - 1]?.nextCursor !== undefined;
              return (
                <p className="manage-section__description">
                  待处理绑定问题：{loaded}
                  {more ? ' 条（还有更多，未全部载入）' : ' 条'}
                </p>
              );
            }}
          </AsyncPanel>
          <AsyncPanel query={orphans}>
            {(data) => {
              const loaded = data.pages.reduce((sum, page) => sum + page.candidates.length, 0);
              const more = data.pages[data.pages.length - 1]?.nextCursor !== undefined;
              return (
                <p className="manage-section__description">
                  孤儿候选：{loaded}
                  {more ? ' 条（还有更多，未全部载入）' : ' 条'}
                </p>
              );
            }}
          </AsyncPanel>
          <p className="manage-section__description">
            <Link to="/governance">前往治理</Link>
          </p>
        </Section>
      </div>

      <Section
        title="当前主体的 global capability"
        description="这是 bootstrap 返回的 global scope 能力集合，不包含按 Library/Source 的授权。它只用于隐藏明显不可用的入口。"
      >
        {capabilities.size === 0 ? (
          <Absent>当前主体在 global scope 没有任何 capability</Absent>
        ) : (
          <p className="manage-status-bar">
            {[...capabilities].map((name) => (
              <Badge key={name}>{name}</Badge>
            ))}
          </p>
        )}
        <p className="manage-section__description">
          当前主体的角色 capability 上限共 {bootstrap.availableCapabilities.length} 项。
        </p>
      </Section>

      <Section
        title="契约事实与已知缺口"
        description="以下内容是服务端当前的真实行为，不是本界面的实现进度。"
      >
        <ContractNoteList area="authorization" />
        <ContractNoteList area="realtime" />
        <ContractNoteList area="resources" />
        <ContractNoteList area="diagnostics" />
      </Section>
    </>
  );
}
