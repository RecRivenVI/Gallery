import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { WorkCover } from './work-cover';

afterEach(cleanup);

describe('WorkCover', () => {
  it('只用同一 publication 构造媒体 URL，并在加载失败时移除坏图', () => {
    render(
      <WorkCover
        title="合成作品"
        coverMediaId="media_01SYNTHETIC"
        queryPublicationId="qpub_01SYNTHETIC"
        canReadMedia
        alt="合成作品封面"
      />
    );

    const image = screen.getByRole('img', { name: '合成作品封面' });
    expect(image).toHaveAttribute(
      'src',
      '/api/v1/media/media_01SYNTHETIC/content?queryPublicationId=qpub_01SYNTHETIC'
    );
    expect(image).toHaveAttribute('loading', 'lazy');

    fireEvent.error(image);

    expect(screen.queryByRole('img', { name: '合成作品封面' })).not.toHaveAttribute('src');
    expect(screen.getByText('合')).toBeVisible();
  });

  it('无 media.read 或无封面时只显示首字母占位', () => {
    const { rerender } = render(
      <WorkCover
        title="Archive"
        coverMediaId="media_01SYNTHETIC"
        queryPublicationId="qpub_01SYNTHETIC"
        canReadMedia={false}
      />
    );
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByText('A')).toBeVisible();

    rerender(
      <WorkCover title="画廊" coverMediaId={null} queryPublicationId="qpub_01SYNTHETIC" canReadMedia />
    );
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByText('画')).toBeVisible();
  });
});
