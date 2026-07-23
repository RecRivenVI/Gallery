import { useQuery } from '@tanstack/react-query';
import {
  Button,
  Form,
  Input,
  Label,
  Select,
  SelectValue,
  ListBox,
  ListBoxItem,
  Popover,
  TextField
} from 'react-aria-components';
import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api, expectData } from '../api/client';
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '../components/ui';

export function BrowsePage() {
  const [params, setParams] = useSearchParams();
  const q = params.get('q') ?? '';
  const tag = params.get('tag') ?? '';
  const sortDirection = params.get('direction') === 'desc' ? 'desc' : 'asc';
  const cursor = params.get('cursor') ?? undefined;
  const publication = params.get('publication') ?? undefined;
  const [draft, setDraft] = useState(q);
  const works = useQuery({
    queryKey: ['works', q, tag, sortDirection, cursor, publication],
    queryFn: async ({ signal }) =>
      expectData(
        await api.GET('/api/v1/works', {
          signal,
          params: {
            query: {
              q: q || undefined,
              tag: tag || undefined,
              sortDirection,
              cursor,
              queryPublicationId: publication,
              limit: 48
            }
          }
        })
      ),
    placeholderData: (previous) => previous
  });

  const navigate = (changes: Record<string, string | undefined>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(changes)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next, { replace: false });
  };

  return (
    <>
      <PageHeader
        title="浏览作品"
        description="列表、排序、过滤和分页全部由服务端 publication 决定；客户端不会重排结果。"
        actions={
          works.data && (
            <StatusBadge tone="info">
              {works.data.total.mode === 'omitted' ? '未计数' : `${works.data.total.value ?? 0} 项`}
            </StatusBadge>
          )
        }
      />
      <Form
        className="panel toolbar"
        role="search"
        onSubmit={(event) => {
          event.preventDefault();
          navigate({ q: draft.trim() || undefined, cursor: undefined, publication: undefined });
        }}
      >
        <TextField value={draft} onChange={setDraft}>
          <Label>搜索</Label>
          <Input placeholder="标题、创作者、标签或文件名" />
        </TextField>
        <TextField
          value={tag}
          onChange={(value) =>
            navigate({ tag: value || undefined, cursor: undefined, publication: undefined })
          }
        >
          <Label>标签</Label>
          <Input />
        </TextField>
        <Select
          selectedKey={sortDirection}
          onSelectionChange={(key) =>
            navigate({ direction: String(key), cursor: undefined, publication: undefined })
          }
        >
          <Label>方向</Label>
          <Button className="select-button">
            <SelectValue />
          </Button>
          <Popover>
            <ListBox>
              <ListBoxItem id="asc">升序</ListBoxItem>
              <ListBoxItem id="desc">降序</ListBoxItem>
            </ListBox>
          </Popover>
        </Select>
        <Button type="submit" className="button primary">
          应用
        </Button>
        {(q || tag || cursor) && (
          <Button
            className="button ghost"
            onPress={() => {
              setDraft('');
              setParams({});
            }}
          >
            清除
          </Button>
        )}
      </Form>
      {works.isPending ? (
        <LoadingState />
      ) : works.isError ? (
        <ErrorState error={works.error} onRetry={() => void works.refetch()} />
      ) : works.data.works.length === 0 ? (
        <EmptyState title="没有匹配的作品" detail="尝试缩短过滤条件，或先完成 Source 扫描与 publication。" />
      ) : (
        <>
          <p className="snapshot-note">
            快照 <code>{works.data.queryPublicationId}</code> · Catalog {works.data.catalogRevision} · Overlay{' '}
            {works.data.overlayProjectionRevision}
          </p>
          <div className="work-grid">
            {works.data.works.map((work) => (
              <article className="work-card" key={work.id}>
                <div className="work-cover" aria-hidden="true">
                  {work.title.slice(0, 1).toUpperCase()}
                </div>
                <div>
                  <h2>
                    <Link
                      to={`/works/${encodeURIComponent(work.id)}?publication=${encodeURIComponent(works.data.queryPublicationId)}`}
                    >
                      {work.title}
                    </Link>
                  </h2>
                  <p>
                    {work.creator || '未知创作者'} · {work.mediaCount} 个媒体
                  </p>
                  <div className="tag-row">
                    {work.tags.slice(0, 4).map((item) => (
                      <span key={item}>{item}</span>
                    ))}
                    {work.favorite && <span>收藏</span>}
                  </div>
                </div>
              </article>
            ))}
          </div>
          <div className="pagination">
            <Button
              className="button secondary"
              isDisabled={!cursor}
              onPress={() => navigate({ cursor: undefined, publication: undefined })}
            >
              回到第一页
            </Button>
            <Button
              className="button primary"
              isDisabled={!works.data.nextCursor}
              onPress={() =>
                navigate({ cursor: works.data.nextCursor, publication: works.data.queryPublicationId })
              }
            >
              下一页
            </Button>
          </div>
        </>
      )}
    </>
  );
}
