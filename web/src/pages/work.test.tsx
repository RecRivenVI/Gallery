import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import type { components } from '../api/schema.gen';
import { OverlayEditor } from './work';

type Overlay = components['schemas']['WorkOverlayState'];
type PublishedMedia = components['schemas']['PublishedMedia'];

const initial: Overlay = {
  workId: 'work_01SYNTHETIC',
  titleOverride: '',
  manualTags: [],
  hidden: false,
  customCoverMediaId: 'media_01MISSING',
  favorite: false,
  progress: 0,
  factWatermark: 2,
  queryWatermark: 1,
  projectedWatermark: 1,
  projectionStatus: 'pending'
};

const media: PublishedMedia[] = [
  {
    id: 'media_01FIRST',
    workId: initial.workId,
    kind: 'image',
    mimeType: 'image/jpeg',
    sizeBytes: 100,
    blob: null,
    available: true,
    ordinal: 1,
    queryPublicationId: 'qpub_01SYNTHETIC',
    contentVerificationState: 'located_unverified'
  },
  {
    id: 'media_01SECOND',
    workId: initial.workId,
    kind: 'image',
    mimeType: 'image/png',
    sizeBytes: 200,
    blob: null,
    available: true,
    ordinal: 2,
    queryPublicationId: 'qpub_01SYNTHETIC',
    contentVerificationState: 'located_unverified'
  }
];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('OverlayEditor 封面选择', () => {
  it('显示并清除失效选择，保存当前媒体或规则封面', async () => {
    const user = userEvent.setup();
    const put = vi.spyOn(api, 'PUT').mockResolvedValue({
      data: initial,
      response: new Response(null, { status: 200 })
    } as never);
    renderWithQuery(
      <OverlayEditor
        initial={initial}
        media={media}
        canReadMedia
        publishedCoverMediaId="media_01FIRST"
        csrf="csrf-test"
        onSaved={() => Promise.resolve()}
      />
    );

    expect(screen.getByRole('radio', { name: /已失效的自定义封面/ })).toBeChecked();
    await user.click(screen.getByRole('button', { name: '清除失效选择并使用规则封面' }));
    expect(screen.getByRole('radio', { name: /使用规则封面/ })).toBeChecked();

    await user.click(screen.getByRole('radio', { name: /媒体 #2/ }));
    await user.click(screen.getByRole('button', { name: '保存事实' }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0]?.[0]).toBe('/api/v1/works/{workId}/overlay');
    const selectedBody = (put.mock.calls[0]?.[1] as { body?: { customCoverMediaId?: string } } | undefined)
      ?.body;
    expect(selectedBody?.customCoverMediaId).toBe('media_01SECOND');

    await user.click(screen.getByRole('radio', { name: /使用规则封面/ }));
    await user.click(screen.getByRole('button', { name: '保存事实' }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(put.mock.calls[1]?.[0]).toBe('/api/v1/works/{workId}/overlay');
    const clearedBody = (put.mock.calls[1]?.[1] as { body?: { customCoverMediaId?: string } } | undefined)
      ?.body;
    expect(clearedBody).toHaveProperty('customCoverMediaId', undefined);
  });
});

function renderWithQuery(children: ReactNode) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      {children}
    </QueryClientProvider>
  );
}
