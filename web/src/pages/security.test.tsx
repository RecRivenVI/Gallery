import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { SecurityAuditSection } from './security';

afterEach(cleanup);

describe('SecurityAuditSection', () => {
  it('对空审计集合显示可访问的 EmptyState，而不是空表格', () => {
    render(<SecurityAuditSection audits={[]} />);

    expect(screen.getByRole('heading', { name: '安全审计' })).toBeVisible();
    expect(screen.getByRole('heading', { name: '没有安全审计' })).toBeVisible();
    expect(screen.getByText('尚未记录可显示的安全事件。')).toBeVisible();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});
