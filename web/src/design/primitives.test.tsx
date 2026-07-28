/*
 * 共享组件的可访问性契约。
 *
 * 这里断言的不是像素，而是「辅助技术能不能用」：可访问名、角色、焦点顺序、错误关联。
 * 视觉回归属于浏览器 E2E，不在 jsdom 里假装验证。
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  Badge,
  Button,
  Checkbox,
  Dialog,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  SkipLink,
  Spinner,
  Switch,
  Tabs,
  TextInput,
  Toast,
  ToastProvider,
  useToast
} from './index';

describe('按钮', () => {
  it('图标按钮把 label 暴露为可访问名', () => {
    render(
      <IconButton label="关闭面板">
        <span aria-hidden="true">✕</span>
      </IconButton>
    );
    expect(screen.getByRole('button', { name: '关闭面板' })).toBeInTheDocument();
  });

  it('isPending 期间不触发 onPress', async () => {
    const onPress = vi.fn();
    render(
      <Button isPending onPress={onPress}>
        提交
      </Button>
    );
    await userEvent.click(screen.getByRole('button', { name: /提交/ }));
    expect(onPress).not.toHaveBeenCalled();
  });

  it('透传 disclosure 控件的展开状态与关联面板', () => {
    render(
      <Button aria-expanded={false} aria-controls="json-tree">
        展开完整 JSON 结构
      </Button>
    );
    const button = screen.getByRole('button', { name: '展开完整 JSON 结构' });
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(button).toHaveAttribute('aria-controls', 'json-tree');
  });
});

describe('SkipLink', () => {
  it('指向主内容并且留在 Tab 序中', () => {
    render(
      <>
        <SkipLink targetId="main" />
        <main id="main">内容</main>
      </>
    );
    const link = screen.getByRole('link', { name: '跳到主内容' });
    expect(link).toHaveAttribute('href', '#main');
    // 未聚焦时靠 transform 移出视口，而不是 display:none —— 后者会让它无法被 Tab 到。
    expect(link).toBeVisible();
  });
});

describe('表单', () => {
  it('TextInput 把错误文案与输入框关联', async () => {
    render(<TextInput label="用户名" errorMessage="用户名不能为空" />);
    const input = screen.getByRole('textbox', { name: /用户名/ });
    await waitFor(() => {
      expect(input).toHaveAttribute('aria-invalid', 'true');
    });
    expect(screen.getByText('用户名不能为空')).toBeInTheDocument();
  });

  it('Field 把 controlId 与 describedBy 交给自建控件', () => {
    render(
      <Field label="规则 JSON" description="保存前后使用同一校验语义" errorMessage="无法解析">
        {({ controlId, describedBy, isInvalid }) => (
          <textarea id={controlId} aria-describedby={describedBy} aria-invalid={isInvalid} />
        )}
      </Field>
    );
    const control = screen.getByRole('textbox', { name: '规则 JSON' });
    expect(control).toHaveAttribute('aria-invalid', 'true');
    const describedBy = control.getAttribute('aria-describedby') ?? '';
    // 说明与错误都必须进 aria-describedby，否则屏幕阅读器读不到任何一条。
    expect(describedBy.split(' ')).toHaveLength(2);
  });

  it('Checkbox 与 Switch 暴露正确的角色和选中态', async () => {
    render(
      <>
        <Checkbox defaultSelected>包含未确认媒体</Checkbox>
        <Switch>实时刷新</Switch>
      </>
    );
    expect(screen.getByRole('checkbox', { name: '包含未确认媒体' })).toBeChecked();
    const toggle = screen.getByRole('switch', { name: '实时刷新' });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    expect(toggle).toBeChecked();
  });
});

describe('弹出层与页签', () => {
  it('Dialog 由触发按钮打开并暴露 dialog 角色', async () => {
    render(
      <Dialog
        title="确认吊销会话"
        trigger={<Button variant="danger">吊销</Button>}
        footer={(close) => <Button onPress={close}>取消</Button>}
      >
        吊销后该客户端需要重新认证。
      </Dialog>
    );
    await userEvent.click(screen.getByRole('button', { name: '吊销' }));
    const dialog = await screen.findByRole('dialog', { name: '确认吊销会话' });
    expect(dialog).toBeInTheDocument();
  });

  it('受控 Dialog 的 close 能关闭弹窗', async () => {
    function Controlled() {
      const [isOpen, setIsOpen] = useState(true);
      return (
        <Dialog
          title="维护确认"
          isOpen={isOpen}
          onOpenChange={setIsOpen}
          footer={(close) => <Button onPress={close}>知道了</Button>}
        >
          维护期间媒体读取会被暂停。
        </Dialog>
      );
    }
    render(<Controlled />);
    expect(await screen.findByRole('dialog', { name: '维护确认' })).toBeInTheDocument();
    // 受控用法下 Dialog 的 close render prop 同样必须可用，否则页面只能自己维护开合状态。
    await userEvent.click(screen.getByRole('button', { name: '知道了' }));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('Tabs 只渲染选中面板并带上 TabList 名称', async () => {
    render(
      <Tabs
        label="任务视图"
        items={[
          { id: 'running', label: '进行中', content: <p>进行中的任务</p> },
          { id: 'failed', label: '失败', content: <p>失败的任务</p> }
        ]}
      />
    );
    expect(screen.getByRole('tablist', { name: '任务视图' })).toBeInTheDocument();
    expect(screen.getByText('进行中的任务')).toBeInTheDocument();
    expect(screen.queryByText('失败的任务')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('tab', { name: '失败' }));
    expect(screen.getByText('失败的任务')).toBeInTheDocument();
  });
});

describe('状态组件', () => {
  it('Spinner 默认提供可朗读的进度文本', () => {
    render(<Spinner label="正在加载作品" />);
    expect(screen.getByRole('status')).toHaveTextContent('正在加载作品');
  });

  it('Spinner 装饰模式对辅助技术不可见', () => {
    const { container } = render(<Spinner decorative />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull();
  });

  it('ErrorState 是 alert 并展示稳定 code 与关联 ID', () => {
    render(<ErrorState description="Source 当前离线。" code="SOURCE_UNAVAILABLE" correlationId="corr-1" />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Source 当前离线。');
    expect(alert).toHaveTextContent('SOURCE_UNAVAILABLE');
    expect(alert).toHaveTextContent('corr-1');
  });

  it('EmptyState 不冒充错误态', () => {
    render(<EmptyState title="没有匹配的作品" description="换个关键词试试。" />);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '没有匹配的作品' })).toBeInTheDocument();
  });

  it('Badge 可以携带完整语义的可访问名', () => {
    render(
      <Badge tone="warning" aria-label="分级：R18">
        R18
      </Badge>
    );
    expect(screen.getByLabelText('分级：R18')).toHaveTextContent('R18');
  });
});

describe('Toast', () => {
  it('危险级用 alert 打断，其余用 status 礼貌播报', () => {
    const { rerender } = render(<Toast title="任务失败" tone="danger" />);
    expect(screen.getByRole('alert')).toHaveTextContent('任务失败');
    rerender(<Toast title="已保存" tone="success" />);
    expect(screen.getByRole('status')).toHaveTextContent('已保存');
  });

  it('ToastProvider 的 show/dismiss 可以增删通知', async () => {
    function Trigger() {
      const { show } = useToast();
      return <Button onPress={() => show({ title: '扫描已排队', tone: 'info' })}>排队扫描</Button>;
    }
    render(
      <ToastProvider>
        <Trigger />
      </ToastProvider>
    );
    expect(screen.queryByText('扫描已排队')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '排队扫描' }));
    expect(screen.getByText('扫描已排队')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '关闭通知' }));
    expect(screen.queryByText('扫描已排队')).not.toBeInTheDocument();
  });
});
