/*
 * 用户事实（Overlay）编辑。
 *
 * 三条契约事实决定了这里的形状，请不要"优化"掉：
 *
 * 1. **PUT 是整体替换**，请求体必须带齐 titleOverride / manualTags / hidden / favorite /
 *    progress。因此编辑器总是基于一次 GET 的完整状态提交，而不是提交"我改了哪几个字段"。
 * 2. **没有 If-Match/ETag**。两个页签同时改不同字段会互相覆盖，这是已知的契约限制。
 *    界面能做的是：保存后一律以服务端响应为准并重新读取，如实显示最终结果，
 *    绝不乐观地把本地值当成最终值。
 * 3. **写入是同步的，查询投影是异步的**。favorite/progress 有 live 读取通道，改完立刻
 *    可见；titleOverride/manualTags/hidden/customCover 只有等 Overlay 重新投影并发布新
 *    publication，列表的过滤与排序才会跟着变。`projectionStatus` 就是用来解释这件事的，
 *    必须显示出来，否则用户会以为保存失败。
 */

import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import {
  Button,
  Checkbox,
  ErrorState,
  Field,
  Select,
  Spinner,
  Switch,
  TextInput,
  useToast
} from '../../design';
import { describeError, errorCode, errorCorrelationId } from '../../shared/errors';
import { formatProgress, publicationHref, type PublishedMedia, type WorkOverlayState } from '../contracts';
import { useOverlayMutation, useWorkOverlay } from '../queries';

export interface OverlayEditorProps {
  workId: string;
  /** 当前详情页绑定的 publication；写后仍保持不漂移，并显式提供新快照入口。 */
  queryPublicationId: string;
  /** 可选作自定义封面的媒体。来自同一个 publication 的媒体列表。 */
  media: readonly PublishedMedia[];
  /** 只有媒体快照成功读取后，空列表才能证明 CustomCover 已失效。 */
  mediaReady: boolean;
}

interface Draft {
  titleOverride: string;
  manualTags: string;
  hidden: boolean;
  favorite: boolean;
  progress: number;
  customCoverMediaId: string;
}

const NO_COVER = 'none';

function toDraft(state: WorkOverlayState): Draft {
  return {
    titleOverride: state.titleOverride,
    manualTags: state.manualTags.join('，'),
    hidden: state.hidden,
    favorite: state.favorite,
    progress: state.progress,
    customCoverMediaId: state.customCoverMediaId ?? NO_COVER
  };
}

/** 手动标签用中英文逗号或换行分隔。空白项直接丢弃，不产生空标签。 */
function parseTags(value: string): string[] {
  return value
    .split(/[,，\n]/)
    .map((tag) => tag.trim())
    .filter((tag) => tag !== '');
}

export function OverlayEditor({ workId, queryPublicationId, media, mediaReady }: OverlayEditorProps) {
  const overlay = useWorkOverlay(workId, true);
  const mutation = useOverlayMutation(workId);
  const toast = useToast();
  const [draft, setDraft] = useState<Draft | undefined>(undefined);
  const [dirty, setDirty] = useState(false);

  const state = overlay.data;
  useEffect(() => {
    // 首次加载和没有本地编辑时以服务端为准；后台 publication 只改变投影状态时，
    // 不能静默抹掉用户尚未保存的标题、标签、进度或封面选择。
    if (state) {
      setDraft((current) => (current === undefined || !dirty ? toDraft(state) : current));
    }
  }, [dirty, state]);

  if (overlay.isPending) {
    return (
      <div className="gal-panel">
        <Spinner label="正在读取用户事实" />
      </div>
    );
  }
  if (!state || !draft) {
    return (
      <ErrorState
        title="无法读取用户事实"
        description={describeError(overlay.error)}
        code={errorCode(overlay.error)}
        correlationId={errorCorrelationId(overlay.error)}
        onRetry={() => void overlay.refetch()}
      />
    );
  }

  const publishedPublicationId = state.publishedQueryPublicationId;
  const missingCoverMediaId =
    mediaReady &&
    state.customCoverMediaId !== undefined &&
    !media.some((item) => item.id === state.customCoverMediaId)
      ? state.customCoverMediaId
      : undefined;
  const invalidCoverMediaId =
    state.projectionStatus === 'published' && publishedPublicationId === queryPublicationId
      ? missingCoverMediaId
      : undefined;
  const historicalCoverMediaId =
    missingCoverMediaId !== undefined &&
    publishedPublicationId !== undefined &&
    publishedPublicationId !== queryPublicationId
      ? missingCoverMediaId
      : undefined;
  const unavailableCoverMediaId = invalidCoverMediaId ?? historicalCoverMediaId ?? missingCoverMediaId;
  const coverOptions = [
    { id: NO_COVER, label: '不指定（使用规则解析的封面）' },
    ...(unavailableCoverMediaId === undefined
      ? []
      : [
          {
            id: unavailableCoverMediaId,
            label:
              invalidCoverMediaId === undefined
                ? '当前自定义封面属于另一快照（可打开新版本查看）'
                : '当前自定义封面已失效（请选择替代项或清除）',
            isDisabled: true
          }
        ]),
    ...media.map((item) => ({ id: item.id, label: `第 ${item.ordinal + 1} 项 · ${item.mimeType}` }))
  ];
  const hasNewPublication =
    state.projectionStatus === 'published' &&
    publishedPublicationId !== undefined &&
    publishedPublicationId !== '' &&
    publishedPublicationId !== queryPublicationId;

  return (
    <form
      className="gal-panel gal-overlay-editor"
      onSubmit={(event) => {
        event.preventDefault();
        mutation.mutate(
          {
            titleOverride: draft.titleOverride,
            manualTags: parseTags(draft.manualTags),
            hidden: draft.hidden,
            favorite: draft.favorite,
            progress: draft.progress,
            customCoverMediaId: draft.customCoverMediaId === NO_COVER ? null : draft.customCoverMediaId
          },
          {
            onSuccess: (next) => {
              setDraft(toDraft(next));
              setDirty(false);
              toast.show({
                title: '用户事实已保存',
                description:
                  next.projectionStatus === 'published'
                    ? '查询投影已是最新。'
                    : '列表的过滤与排序要等 Overlay 重新投影后才会跟着变化。',
                tone: 'success'
              });
            },
            onError: (error: unknown) => {
              toast.show({ title: '保存失败', description: describeError(error), tone: 'danger' });
            }
          }
        );
      }}
    >
      <h2 className="gal-panel__title">我的编辑</h2>
      <p className="gal-muted">
        这些是<strong>你的</strong>事实，重新扫描不会覆盖它们；它们与规则派生的标题、标签、角标是两回事。
      </p>

      <Switch
        isSelected={draft.favorite}
        onChange={(value) => {
          setDraft({ ...draft, favorite: value });
          setDirty(true);
        }}
      >
        收藏
      </Switch>

      <Field
        label="阅读进度"
        description={`当前 ${formatProgress(draft.progress)}。与收藏一样是实时值，保存后立即生效。`}
      >
        {({ controlId, describedBy }) => (
          <input
            id={controlId}
            aria-describedby={describedBy}
            className="gal-range"
            type="range"
            min={0}
            max={100}
            step={1}
            value={Math.round(draft.progress * 100)}
            onChange={(event) => {
              setDraft({ ...draft, progress: Number(event.target.value) / 100 });
              setDirty(true);
            }}
          />
        )}
      </Field>

      <TextInput
        label="标题覆盖"
        value={draft.titleOverride}
        onChange={(value) => {
          setDraft({ ...draft, titleOverride: value });
          setDirty(true);
        }}
        description="留空表示使用规则解析出的标题。"
      />

      <TextInput
        label="手动标签"
        value={draft.manualTags}
        onChange={(value) => {
          setDraft({ ...draft, manualTags: value });
          setDirty(true);
        }}
        description="用逗号分隔。手动标签与规则标签并存，不会互相覆盖。"
      />

      <Select
        label="自定义封面"
        options={coverOptions}
        selectedKey={draft.customCoverMediaId}
        isDisabled={!mediaReady}
        onSelectionChange={(key) => {
          setDraft({ ...draft, customCoverMediaId: key ?? NO_COVER });
          setDirty(true);
        }}
        description={
          !mediaReady
            ? '媒体列表尚未加载，暂时不能判断或修改自定义封面。'
            : invalidCoverMediaId !== undefined
              ? '当前选择已经不在本快照的媒体中，展示已回退到规则封面；请选择替代项或清除。'
              : historicalCoverMediaId !== undefined
                ? '当前选择属于另一个已投影快照；本页是历史版本，不能据此判断封面失效。'
                : '自定义封面优先于规则封面；指定的媒体失效时会自动回退。'
        }
      />

      <Checkbox
        isSelected={draft.hidden}
        onChange={(value) => {
          setDraft({ ...draft, hidden: value });
          setDirty(true);
        }}
      >
        在浏览中隐藏这个作品
      </Checkbox>

      <p className="gal-muted" role="status" aria-live="polite" aria-atomic="true">
        投影状态：{projectionText(state)}。契约没有版本冲突检测，多个页签同时编辑会互相覆盖；
        保存后显示的始终是服务端返回的最终结果。
        {hasNewPublication ? ' 已生成新快照，可通过下方链接打开。' : ''}
      </p>

      {hasNewPublication ? (
        <p>
          当前页仍固定在保存前的快照。{' '}
          <Link
            className="gal-link"
            to={publicationHref(`/works/${encodeURIComponent(workId)}`, publishedPublicationId)}
          >
            打开已投影版本
          </Link>
        </p>
      ) : null}

      <Button type="submit" variant="primary" isPending={mutation.isPending}>
        保存
      </Button>
    </form>
  );
}

function projectionText(state: WorkOverlayState): string {
  switch (state.projectionStatus) {
    case 'published':
      return '已投影到查询快照';
    case 'pending':
      return '重新投影排队中，列表暂时仍是上一次的结果';
    case 'failed':
      return `投影失败（${state.issueCode ?? '未提供原因'}），列表仍是上一次成功的快照`;
  }
}
