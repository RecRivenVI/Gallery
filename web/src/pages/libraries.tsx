import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Form, Input, Label, TextField } from 'react-aria-components';
import { useState } from 'react';
import { api, csrfHeaders, errorMessage, expectData } from '../api/client';
import { useSession } from '../auth/session';
import {
  DefinitionList,
  EmptyState,
  ErrorState,
  formatDate,
  LoadingState,
  PageHeader,
  StatusBadge
} from '../components/ui';

export function LibrariesPage() {
  const { bootstrap, can } = useSession();
  const client = useQueryClient();
  const [libraryName, setLibraryName] = useState('');
  const [sourceName, setSourceName] = useState('');
  const [rootPath, setRootPath] = useState('');
  const [libraryId, setLibraryId] = useState('');
  const libraries = useQuery({
    queryKey: ['libraries'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/libraries', { signal }))
  });
  const sources = useQuery({
    queryKey: ['sources'],
    queryFn: async ({ signal }) => expectData(await api.GET('/api/v1/sources', { signal }))
  });
  const createLibrary = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/libraries', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: { name: libraryName }
        })
      ),
    onSuccess: async (created) => {
      setLibraryName('');
      setLibraryId(created.id);
      await client.invalidateQueries({ queryKey: ['libraries'] });
    }
  });
  const createSource = useMutation({
    mutationFn: async () =>
      expectData(
        await api.POST('/api/v1/sources', {
          params: { header: csrfHeaders(bootstrap.csrfToken) },
          body: { libraryId, displayName: sourceName, rootPath }
        })
      ),
    onSuccess: async () => {
      setSourceName('');
      setRootPath('');
      await client.invalidateQueries({ queryKey: ['sources'] });
    }
  });
  const scan = useMutation({
    mutationFn: async (sourceId: string) =>
      expectData(
        await api.POST('/api/v1/sources/{sourceId}/scan-jobs', {
          params: {
            path: { sourceId },
            header: { ...csrfHeaders(bootstrap.csrfToken), 'Idempotency-Key': crypto.randomUUID() }
          },
          body: { scanProfile: 'incremental' }
        })
      )
  });
  if (libraries.isPending || sources.isPending) return <LoadingState />;
  if (libraries.isError) return <ErrorState error={libraries.error} />;
  if (sources.isError) return <ErrorState error={sources.error} />;
  return (
    <>
      <PageHeader
        title="资料库与 Source"
        description="媒体根永久只读；浏览器只登记根目录，不会直接访问本机文件系统。"
      />
      {can('library.write') && (
        <div className="detail-layout">
          <Form
            className="panel form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              createLibrary.mutate();
            }}
          >
            <h2>新建资料库</h2>
            <TextField isRequired value={libraryName} onChange={setLibraryName}>
              <Label>名称</Label>
              <Input />
            </TextField>
            <Button type="submit" className="button primary">
              创建
            </Button>
          </Form>
          <Form
            className="panel form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              createSource.mutate();
            }}
          >
            <h2>登记只读 Source</h2>
            <TextField isRequired value={libraryId} onChange={setLibraryId}>
              <Label>Library ID</Label>
              <Input />
            </TextField>
            <TextField isRequired value={sourceName} onChange={setSourceName}>
              <Label>显示名称</Label>
              <Input />
            </TextField>
            <TextField isRequired value={rootPath} onChange={setRootPath}>
              <Label>服务端绝对根路径</Label>
              <Input autoComplete="off" />
            </TextField>
            <Button type="submit" className="button primary">
              登记 Source
            </Button>
            <p className="callout warning">路径只发送给服务端，本页和列表不会回显绝对路径。</p>
          </Form>
        </div>
      )}
      {(createLibrary.isError || createSource.isError || scan.isError) && (
        <p className="field-error" role="alert">
          {errorMessage(createLibrary.error ?? createSource.error ?? scan.error)}
        </p>
      )}
      <h2>Library</h2>
      {libraries.data.libraries.length === 0 ? (
        <EmptyState title="尚无 Library" detail="先创建一个用户事实资料库。" />
      ) : (
        <div className="card-grid">
          {libraries.data.libraries.map((library) => (
            <article className="card" key={library.id}>
              <h3>{library.name}</h3>
              <DefinitionList
                items={[
                  ['ID', <code>{library.id}</code>],
                  ['创建', formatDate(library.createdAt)]
                ]}
              />
            </article>
          ))}
        </div>
      )}
      <h2>Source</h2>
      {sources.data.sources.length === 0 ? (
        <EmptyState title="尚无 Source" detail="登记一个合成或明确授权的只读媒体根。" />
      ) : (
        <div className="card-grid">
          {sources.data.sources.map((source) => (
            <article className="card" key={source.id}>
              <h3>{source.displayName}</h3>
              <StatusBadge tone={source.available ? 'success' : 'warning'}>
                {source.available ? '在线' : '离线'}
              </StatusBadge>
              <DefinitionList
                items={[
                  ['ID', <code>{source.id}</code>],
                  ['Library', <code>{source.libraryId}</code>],
                  ['只读', source.readOnly ? '是' : '否']
                ]}
              />
              {can('scan.run') && (
                <Button className="button secondary" onPress={() => scan.mutate(source.id)}>
                  增量扫描
                </Button>
              )}
            </article>
          ))}
        </div>
      )}
    </>
  );
}
