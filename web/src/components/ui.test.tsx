import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { EmptyState, ProgressBar } from './ui';

describe('共享 UI', () => {
  it('为空状态提供清晰标题', () => {
    render(
      <MemoryRouter>
        <EmptyState title="没有作品" detail="先扫描 Source" />
      </MemoryRouter>
    );
    expect(screen.getByRole('heading', { name: '没有作品' })).toBeInTheDocument();
  });

  it('把进度限制在可访问范围', () => {
    render(<ProgressBar value={2} label="完成" />);
    expect(screen.getByRole('progressbar', { name: '完成' })).toHaveAttribute('aria-valuenow', '100');
  });
});
