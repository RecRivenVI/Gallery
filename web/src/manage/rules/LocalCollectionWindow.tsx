import { useEffect, useState } from 'react';
import { Button } from '../../design';

export const LOCAL_COLLECTION_PAGE_SIZE = 20;

export interface LocalCollectionWindowState {
  itemCount: number;
  pageSize: number;
  pageCount: number;
  pageIndex: number;
  start: number;
  end: number;
  visibleCount: number;
  setPageIndex: (pageIndex: number) => void;
  showIndex: (itemIndex: number) => void;
}

/**
 * 规则配置属于一个本地精确草稿，不存在服务端分页。这里仅限制当前挂载的编辑器子树；
 * 完整数组/对象仍留在同一份 formData 中，保存、校验、移动和删除继续按全局索引执行。
 */
export function useLocalCollectionWindow(
  itemCount: number,
  pageSize = LOCAL_COLLECTION_PAGE_SIZE
): LocalCollectionWindowState {
  const [requestedPageIndex, setRequestedPageIndex] = useState(0);
  const pageCount = Math.max(1, Math.ceil(itemCount / pageSize));
  const pageIndex = Math.min(requestedPageIndex, pageCount - 1);

  useEffect(() => {
    if (requestedPageIndex !== pageIndex) setRequestedPageIndex(pageIndex);
  }, [pageIndex, requestedPageIndex]);

  const start = pageIndex * pageSize;
  const end = Math.min(itemCount, start + pageSize);

  return {
    itemCount,
    pageSize,
    pageCount,
    pageIndex,
    start,
    end,
    visibleCount: Math.max(0, end - start),
    setPageIndex: (nextPageIndex) =>
      setRequestedPageIndex(Math.max(0, Math.min(nextPageIndex, pageCount - 1))),
    showIndex: (itemIndex) =>
      setRequestedPageIndex(Math.max(0, Math.floor(Math.max(0, itemIndex) / pageSize)))
  };
}

export function LocalCollectionPager({
  label,
  window
}: {
  label: string;
  window: LocalCollectionWindowState;
}) {
  if (window.itemCount <= window.pageSize) return null;

  return (
    <div className="manage-local-window">
      <p className="manage-section__description">
        {label} · 第 {window.pageIndex + 1} / {window.pageCount} 页 · 本页 {window.visibleCount} 项 · 共{' '}
        {window.itemCount} 项 · 每页最多 {window.pageSize} 项。
      </p>
      <nav className="manage-form__actions" aria-label={`${label} 分页`}>
        <Button
          variant="secondary"
          isDisabled={window.pageIndex === 0}
          aria-label={`上一页：${label}`}
          onPress={() => window.setPageIndex(window.pageIndex - 1)}
        >
          上一页
        </Button>
        <Button
          variant="secondary"
          isDisabled={window.pageIndex >= window.pageCount - 1}
          aria-label={`下一页：${label}`}
          onPress={() => window.setPageIndex(window.pageIndex + 1)}
        >
          下一页
        </Button>
      </nav>
    </div>
  );
}
