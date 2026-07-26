/*
 * Gallery 共享组件。
 *
 * 画廊与管理两套界面共用**同一批**组件；差异只体现在密度 token 与组合方式上，任何一端都不得
 * 私自复制一份外观不同的按钮或输入框。
 *
 * 交互与可访问性一律交给 react-aria-components（RAC）：键盘导航、焦点收束、aria 关联、
 * 触控与指针差异都由它负责，本文件只提供外观与中文文案。**不要**把 RAC 换成手写实现，
 * 也不要升级 react-aria-components 的版本——`internal/webapp/handler.go` 的 CSP 里放行了
 * 一段与该版本绑定的 RAC 内联样式哈希，升级会让移动端触控行为被 CSP 拦掉。
 *
 * 唯一没有使用 RAC 的是 Toast：RAC 1.19 只提供 `UNSTABLE_Toast*`，前缀本身就是「随时可能
 * 变形」的声明，不适合作为两条并行工作线共同依赖的契约。这里改用一个语义正确的 aria live
 * region（危险级用 role="alert"，其余用 role="status"），关闭按钮仍是 RAC Button。
 */

import {
  Button as AriaButton,
  Checkbox as AriaCheckbox,
  Dialog as AriaDialog,
  Menu as AriaMenu,
  Select as AriaSelect,
  Switch as AriaSwitch,
  Tabs as AriaTabs,
  Tooltip as AriaTooltip,
  DialogTrigger,
  FieldError,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  MenuItem,
  MenuTrigger,
  Modal,
  ModalOverlay,
  Popover,
  SelectValue,
  Tab,
  TabList,
  TabPanel,
  Text,
  TextArea,
  TextField,
  TooltipTrigger,
  VisuallyHidden as AriaVisuallyHidden
} from 'react-aria-components';
import { createContext, useCallback, useContext, useId, useMemo, useRef, useState } from 'react';
import type { Key, ReactNode } from 'react';
import './primitives.css';

/* ————————————————————————————— 通用类型 ————————————————————————————— */

/** 语义色调。与 tokens.css 的 --color-* 一一对应，不允许出现表格之外的第五种颜色。 */
export type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';

function classes(...values: (string | false | undefined)[]): string {
  return values.filter((value): value is string => typeof value === 'string' && value.length > 0).join(' ');
}

/* ————————————————————————————— 无障碍基元 ————————————————————————————— */

/** 只对辅助技术可见的文本。用于给纯图标控件、Spinner 与状态变化补充可读描述。 */
export function VisuallyHidden({ children }: { children: ReactNode }) {
  return <AriaVisuallyHidden>{children}</AriaVisuallyHidden>;
}

export interface SkipLinkProps {
  /** 目标元素的 DOM id，通常是主内容区。 */
  targetId: string;
  children?: ReactNode;
}

/**
 * 跳转到主内容的链接。必须是页面 DOM 中的第一个可聚焦元素，否则键盘用户每次导航都要
 * 穿过整个导航区。未聚焦时用 transform 移出视口而不是 display:none——后者会把它移出 Tab 序。
 */
export function SkipLink({ targetId, children = '跳到主内容' }: SkipLinkProps) {
  return (
    <a className="ui-skip-link" href={`#${targetId}`}>
      {children}
    </a>
  );
}

/* ————————————————————————————— 按钮 ————————————————————————————— */

export interface ButtonProps {
  children: ReactNode;
  variant?: ButtonVariant;
  type?: 'button' | 'submit' | 'reset';
  onPress?: () => void;
  isDisabled?: boolean;
  /** 提交中。RAC 会同时置 aria-disabled 并保留焦点，不要用 isDisabled 表达「进行中」。 */
  isPending?: boolean;
  /** 占满容器宽度，用于表单主操作与窄屏。 */
  isBlock?: boolean;
  autoFocus?: boolean;
  'aria-label'?: string;
  'aria-describedby'?: string;
  className?: string;
}

export function Button({
  children,
  variant = 'secondary',
  type = 'button',
  onPress,
  isDisabled,
  isPending,
  isBlock,
  autoFocus,
  className,
  ...aria
}: ButtonProps) {
  return (
    <AriaButton
      type={type}
      onPress={onPress}
      isDisabled={isDisabled}
      isPending={isPending}
      autoFocus={autoFocus}
      aria-label={aria['aria-label']}
      aria-describedby={aria['aria-describedby']}
      className={classes('ui-button', `ui-button--${variant}`, isBlock && 'ui-button--block', className)}
    >
      {isPending ? <Spinner decorative /> : null}
      {children}
    </AriaButton>
  );
}

export interface IconButtonProps extends Omit<ButtonProps, 'children' | 'isBlock' | 'aria-label'> {
  /** 图标按钮没有可见文本，label 会成为 aria-label，必填。 */
  label: string;
  children: ReactNode;
}

/** 纯图标按钮。宽高固定为 --control-height，保证触控目标不因 compact 密度塌陷。 */
export function IconButton({ label, children, className, ...rest }: IconButtonProps) {
  return (
    <Button {...rest} aria-label={label} className={classes('ui-icon-button', className)}>
      {children}
    </Button>
  );
}

/* ————————————————————————————— 表单 ————————————————————————————— */

export interface FieldRenderProps {
  /** 控件必须使用这个 id，label 的 for 已经指向它。 */
  controlId: string;
  /** 控件必须把它放进 aria-describedby，否则说明与错误文本不会被朗读。 */
  describedBy: string | undefined;
  isInvalid: boolean;
}

export interface FieldProps {
  label: ReactNode;
  description?: ReactNode;
  /** 有值即视为无效状态。 */
  errorMessage?: ReactNode;
  isRequired?: boolean;
  children: (render: FieldRenderProps) => ReactNode;
  className?: string;
}

/**
 * 通用字段外壳，供自建控件（例如规则配置编辑器）使用。
 *
 * children 是 render prop 而不是普通节点：label/description/error 与控件之间的 aria 关联
 * 只能由控件自己挂上，做成普通节点就一定会漏掉 aria-describedby。标准文本框与下拉框请直接
 * 用 TextInput / Select，它们由 RAC 负责这套关联。
 */
export function Field({ label, description, errorMessage, isRequired, children, className }: FieldProps) {
  const baseId = useId();
  const controlId = `${baseId}-control`;
  const descriptionId = `${baseId}-description`;
  const errorId = `${baseId}-error`;
  const isInvalid = errorMessage !== undefined && errorMessage !== null && errorMessage !== '';
  const describedBy =
    classes(description ? descriptionId : undefined, isInvalid ? errorId : undefined) || undefined;

  return (
    <div className={classes('ui-field', className)}>
      <label className="ui-field__label" htmlFor={controlId}>
        {label}
        {isRequired ? (
          <span className="ui-field__required" aria-hidden="true">
            *
          </span>
        ) : null}
      </label>
      {children({ controlId, describedBy, isInvalid })}
      {description ? (
        <p className="ui-field__description" id={descriptionId}>
          {description}
        </p>
      ) : null}
      {isInvalid ? (
        <p className="ui-field__error" id={errorId} role="alert">
          {errorMessage}
        </p>
      ) : null}
    </div>
  );
}

export interface TextInputProps {
  label: ReactNode;
  value?: string;
  defaultValue?: string;
  onChange?: (value: string) => void;
  description?: ReactNode;
  errorMessage?: ReactNode;
  placeholder?: string;
  type?: 'text' | 'password' | 'search' | 'email' | 'url';
  /** 多行。规则 JSON、路径等长文本用它，字体自动切到 --font-mono。 */
  isMultiline?: boolean;
  rows?: number;
  isRequired?: boolean;
  isDisabled?: boolean;
  isReadOnly?: boolean;
  autoComplete?: string;
  autoFocus?: boolean;
  name?: string;
  className?: string;
}

export function TextInput({
  label,
  value,
  defaultValue,
  onChange,
  description,
  errorMessage,
  placeholder,
  type = 'text',
  isMultiline,
  rows,
  isRequired,
  isDisabled,
  isReadOnly,
  autoComplete,
  autoFocus,
  name,
  className
}: TextInputProps) {
  const isInvalid = errorMessage !== undefined && errorMessage !== null && errorMessage !== '';
  return (
    <TextField
      className={classes('ui-field', className)}
      value={value}
      defaultValue={defaultValue}
      onChange={onChange}
      type={isMultiline ? undefined : type}
      isRequired={isRequired}
      isDisabled={isDisabled}
      isReadOnly={isReadOnly}
      isInvalid={isInvalid}
      autoComplete={autoComplete}
      name={name}
    >
      <Label className="ui-field__label">
        {label}
        {isRequired ? (
          <span className="ui-field__required" aria-hidden="true">
            *
          </span>
        ) : null}
      </Label>
      {isMultiline ? (
        <TextArea className="ui-textarea" placeholder={placeholder} rows={rows} autoFocus={autoFocus} />
      ) : (
        <Input className="ui-input" placeholder={placeholder} autoFocus={autoFocus} />
      )}
      {description ? (
        <Text className="ui-field__description" slot="description">
          {description}
        </Text>
      ) : null}
      <FieldError className="ui-field__error">{errorMessage}</FieldError>
    </TextField>
  );
}

export interface SelectOption {
  id: string;
  label: string;
  isDisabled?: boolean;
}

export interface SelectProps {
  label: ReactNode;
  options: readonly SelectOption[];
  selectedKey?: string | null;
  defaultSelectedKey?: string;
  onSelectionChange?: (key: string | null) => void;
  description?: ReactNode;
  errorMessage?: ReactNode;
  placeholder?: string;
  isRequired?: boolean;
  isDisabled?: boolean;
  name?: string;
  className?: string;
}

export function Select({
  label,
  options,
  selectedKey,
  defaultSelectedKey,
  onSelectionChange,
  description,
  errorMessage,
  placeholder = '请选择',
  isRequired,
  isDisabled,
  name,
  className
}: SelectProps) {
  const isInvalid = errorMessage !== undefined && errorMessage !== null && errorMessage !== '';
  return (
    <AriaSelect
      className={classes('ui-field', className)}
      selectedKey={selectedKey}
      defaultSelectedKey={defaultSelectedKey}
      onSelectionChange={(key) => onSelectionChange?.(key === null ? null : String(key))}
      isRequired={isRequired}
      isDisabled={isDisabled}
      isInvalid={isInvalid}
      placeholder={placeholder}
      name={name}
    >
      <Label className="ui-field__label">
        {label}
        {isRequired ? (
          <span className="ui-field__required" aria-hidden="true">
            *
          </span>
        ) : null}
      </Label>
      <AriaButton className="ui-select-button">
        <SelectValue />
        <span className="ui-select-arrow" aria-hidden="true">
          ▾
        </span>
      </AriaButton>
      {description ? (
        <Text className="ui-field__description" slot="description">
          {description}
        </Text>
      ) : null}
      <FieldError className="ui-field__error">{errorMessage}</FieldError>
      <Popover className="ui-popover">
        <ListBox className="ui-listbox">
          {options.map((option) => (
            <ListBoxItem
              key={option.id}
              id={option.id}
              textValue={option.label}
              isDisabled={option.isDisabled}
              className="ui-option"
            >
              {option.label}
            </ListBoxItem>
          ))}
        </ListBox>
      </Popover>
    </AriaSelect>
  );
}

export interface CheckboxProps {
  children: ReactNode;
  isSelected?: boolean;
  defaultSelected?: boolean;
  onChange?: (isSelected: boolean) => void;
  isIndeterminate?: boolean;
  isDisabled?: boolean;
  className?: string;
}

export function Checkbox({
  children,
  isSelected,
  defaultSelected,
  onChange,
  isIndeterminate,
  isDisabled,
  className
}: CheckboxProps) {
  return (
    <AriaCheckbox
      className={classes('ui-checkbox', className)}
      isSelected={isSelected}
      defaultSelected={defaultSelected}
      onChange={onChange}
      isIndeterminate={isIndeterminate}
      isDisabled={isDisabled}
    >
      {({ isSelected: checked, isIndeterminate: mixed }) => (
        <>
          <span className="ui-checkbox__box" aria-hidden="true">
            {mixed ? (
              <svg viewBox="0 0 18 18" className="ui-checkbox__mark">
                <line x1="3" y1="9" x2="15" y2="9" />
              </svg>
            ) : checked ? (
              <svg viewBox="0 0 18 18" className="ui-checkbox__mark">
                <polyline points="2,9 7,14 16,4" />
              </svg>
            ) : null}
          </span>
          <span>{children}</span>
        </>
      )}
    </AriaCheckbox>
  );
}

export interface SwitchProps {
  children: ReactNode;
  isSelected?: boolean;
  defaultSelected?: boolean;
  onChange?: (isSelected: boolean) => void;
  isDisabled?: boolean;
  className?: string;
}

/** 开关。用于「立即生效」的偏好；需要显式提交的布尔项请用 Checkbox。 */
export function Switch({
  children,
  isSelected,
  defaultSelected,
  onChange,
  isDisabled,
  className
}: SwitchProps) {
  return (
    <AriaSwitch
      className={classes('ui-switch', className)}
      isSelected={isSelected}
      defaultSelected={defaultSelected}
      onChange={onChange}
      isDisabled={isDisabled}
    >
      <span className="ui-switch__track" aria-hidden="true">
        <span className="ui-switch__thumb" />
      </span>
      <span>{children}</span>
    </AriaSwitch>
  );
}

/* ————————————————————————————— 弹出层 ————————————————————————————— */

export interface DialogProps {
  title: string;
  children: ReactNode | ((close: () => void) => ReactNode);
  footer?: ReactNode | ((close: () => void) => ReactNode);
  /** 非受控用法：把触发按钮交给 RAC，焦点返还由它处理。 */
  trigger?: ReactNode;
  /** 受控用法：与 trigger 二选一。 */
  isOpen?: boolean;
  onOpenChange?: (isOpen: boolean) => void;
  /** 允许点击遮罩或按 Esc 关闭。破坏性确认应设为 false。 */
  isDismissable?: boolean;
  size?: 'sm' | 'md' | 'lg';
}

export function Dialog({
  title,
  children,
  footer,
  trigger,
  isOpen,
  onOpenChange,
  isDismissable = true,
  size = 'md'
}: DialogProps) {
  const overlay = (
    <ModalOverlay
      className="ui-modal-overlay"
      isDismissable={isDismissable}
      isOpen={trigger === undefined ? isOpen : undefined}
      onOpenChange={trigger === undefined ? onOpenChange : undefined}
    >
      <Modal className={classes('ui-modal', size !== 'md' && `ui-modal--${size}`)}>
        <AriaDialog className="ui-dialog" aria-label={title}>
          {({ close }) => (
            <>
              <h2 className="ui-dialog__title" slot="title">
                {title}
              </h2>
              <div className="ui-dialog__body">
                {typeof children === 'function' ? children(close) : children}
              </div>
              {footer === undefined ? null : (
                <div className="ui-dialog__footer">
                  {typeof footer === 'function' ? footer(close) : footer}
                </div>
              )}
            </>
          )}
        </AriaDialog>
      </Modal>
    </ModalOverlay>
  );

  if (trigger === undefined) return overlay;
  return (
    <DialogTrigger>
      {trigger}
      {overlay}
    </DialogTrigger>
  );
}

export interface MenuItemDescriptor {
  id: string;
  label: ReactNode;
  isDisabled?: boolean;
  /** 破坏性操作标红。真正的确认仍应由 Dialog 承担，菜单项本身不做二次确认。 */
  isDestructive?: boolean;
}

export interface MenuProps {
  /** 触发按钮上的可见文本；triggerVariant='icon' 时作为 aria-label。 */
  label: string;
  items: readonly MenuItemDescriptor[];
  onAction: (id: string) => void;
  triggerVariant?: 'button' | 'icon';
  /** triggerVariant='icon' 时显示的图标节点。 */
  icon?: ReactNode;
  buttonVariant?: ButtonVariant;
  isDisabled?: boolean;
}

export function Menu({
  label,
  items,
  onAction,
  triggerVariant = 'button',
  icon = '⋯',
  buttonVariant = 'secondary',
  isDisabled
}: MenuProps) {
  return (
    <MenuTrigger>
      {triggerVariant === 'icon' ? (
        <IconButton label={label} variant={buttonVariant} isDisabled={isDisabled}>
          {icon}
        </IconButton>
      ) : (
        <Button variant={buttonVariant} isDisabled={isDisabled}>
          {label}
        </Button>
      )}
      <Popover className="ui-popover">
        <AriaMenu className="ui-menu" onAction={(key: Key) => onAction(String(key))}>
          {items.map((item) => (
            <MenuItem
              key={item.id}
              id={item.id}
              isDisabled={item.isDisabled}
              className={classes('ui-menu-item', item.isDestructive && 'ui-menu-item--destructive')}
            >
              {item.label}
            </MenuItem>
          ))}
        </AriaMenu>
      </Popover>
    </MenuTrigger>
  );
}

export interface TooltipProps {
  content: ReactNode;
  children: ReactNode;
  placement?: 'top' | 'bottom' | 'start' | 'end';
  /** 悬停多久后出现。默认沿用 RAC 的安全值。 */
  delay?: number;
}

/**
 * 提示气泡。**只用于补充信息**：tooltip 在触屏上不可达，任何操作必需的说明都要写成
 * 可见文本或 Field 的 description。
 *
 * children 必须是可聚焦的触发元素——本目录的 Button / IconButton，或用 RAC 的 Focusable
 * 包过的原生元素。传一段纯文本不会有任何效果，因为键盘用户无法聚焦它。
 */
export function Tooltip({ content, children, placement = 'top', delay }: TooltipProps) {
  return (
    <TooltipTrigger delay={delay}>
      {children}
      <AriaTooltip className="ui-tooltip" placement={placement}>
        {content}
      </AriaTooltip>
    </TooltipTrigger>
  );
}

/* ————————————————————————————— Tabs ————————————————————————————— */

export interface TabDescriptor {
  id: string;
  label: ReactNode;
  content: ReactNode;
  isDisabled?: boolean;
}

export interface TabsProps {
  /** TabList 的无障碍名称，必填：屏幕阅读器需要知道这组标签在切换什么。 */
  label: string;
  items: readonly TabDescriptor[];
  selectedKey?: string;
  defaultSelectedKey?: string;
  onSelectionChange?: (key: string) => void;
  className?: string;
}

export function Tabs({
  label,
  items,
  selectedKey,
  defaultSelectedKey,
  onSelectionChange,
  className
}: TabsProps) {
  return (
    <AriaTabs
      className={classes('ui-tabs', className)}
      selectedKey={selectedKey}
      defaultSelectedKey={defaultSelectedKey}
      onSelectionChange={(key: Key) => onSelectionChange?.(String(key))}
    >
      <TabList className="ui-tablist" aria-label={label}>
        {items.map((item) => (
          <Tab key={item.id} id={item.id} isDisabled={item.isDisabled} className="ui-tab">
            {item.label}
          </Tab>
        ))}
      </TabList>
      {items.map((item) => (
        <TabPanel key={item.id} id={item.id} className="ui-tabpanel">
          {item.content}
        </TabPanel>
      ))}
    </AriaTabs>
  );
}

/* ————————————————————————————— 状态 ————————————————————————————— */

export interface BadgeProps {
  children: ReactNode;
  tone?: Tone;
  /** 供辅助技术朗读的完整语义，例如角标 "R18" 对应 "分级：R18"。 */
  'aria-label'?: string;
}

export function Badge({ children, tone = 'neutral', ...aria }: BadgeProps) {
  return (
    <span className={`ui-badge ui-badge--${tone}`} aria-label={aria['aria-label']}>
      {children}
    </span>
  );
}

export interface SpinnerProps {
  /** 无障碍文本。decorative 为 true 时忽略。 */
  label?: string;
  /**
   * 纯装饰。当外层已经有 role="status" 或按钮自身已表达「进行中」时使用，
   * 避免屏幕阅读器重复朗读。
   */
  decorative?: boolean;
}

export function Spinner({ label = '正在加载…', decorative }: SpinnerProps) {
  if (decorative) return <span className="ui-spinner" aria-hidden="true" />;
  return (
    <span role="status">
      <span className="ui-spinner" aria-hidden="true" />
      <VisuallyHidden>{label}</VisuallyHidden>
    </span>
  );
}

export interface EmptyStateProps {
  title: string;
  description?: ReactNode;
  /** 唯一的下一步动作。没有可执行动作时不要放按钮。 */
  action?: ReactNode;
}

/** 空状态：查询成功但没有结果。不要用它表示「加载中」或「无权限」。 */
export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <section className="ui-state">
      <h2 className="ui-state__title">{title}</h2>
      {description ? <p className="ui-state__description">{description}</p> : null}
      {action}
    </section>
  );
}

export interface ErrorStateProps {
  title?: string;
  /** 已本地化的中文说明。请用 shared/errors.ts 的 describeError/errorCopy 生成。 */
  description: ReactNode;
  /** 服务端稳定 code 与关联 ID，供用户报告问题时引用。 */
  code?: string;
  correlationId?: string;
  onRetry?: () => void;
  retryLabel?: string;
}

/**
 * 错误状态。
 *
 * 刻意不接受 `error: unknown`：design/ 不依赖 shared/，否则设计系统会反向依赖会话与 API 层。
 * 调用方先用 shared/errors.ts 把错误翻成中文，再传进来。
 */
export function ErrorState({
  title = '无法完成请求',
  description,
  code,
  correlationId,
  onRetry,
  retryLabel = '重试'
}: ErrorStateProps) {
  return (
    <section className="ui-state ui-state--error" role="alert">
      <h2 className="ui-state__title">{title}</h2>
      <p className="ui-state__description">{description}</p>
      {code !== undefined || correlationId !== undefined ? (
        <p className="ui-state__code">
          {code ?? ''}
          {code !== undefined && correlationId !== undefined ? ' · ' : ''}
          {correlationId ?? ''}
        </p>
      ) : null}
      {onRetry ? (
        <Button variant="secondary" onPress={onRetry}>
          {retryLabel}
        </Button>
      ) : null}
    </section>
  );
}

/* ————————————————————————————— Toast ————————————————————————————— */

export type ToastTone = 'info' | 'success' | 'warning' | 'danger';

export interface ToastOptions {
  title: string;
  description?: string;
  tone?: ToastTone;
  /** 自动消失时间；0 表示必须手动关闭。危险级默认不自动消失。 */
  timeoutMs?: number;
}

interface ToastRecord extends ToastOptions {
  id: string;
}

export interface ToastProps {
  title: string;
  description?: string;
  tone?: ToastTone;
  onDismiss?: () => void;
}

/** 单条通知的展示组件。危险级用 role="alert" 立即打断，其余用 role="status" 礼貌播报。 */
export function Toast({ title, description, tone = 'info', onDismiss }: ToastProps) {
  return (
    <div className={`ui-toast ui-toast--${tone}`} role={tone === 'danger' ? 'alert' : 'status'}>
      <span className="ui-toast__title">{title}</span>
      {onDismiss ? (
        <IconButton label="关闭通知" variant="ghost" onPress={onDismiss}>
          ✕
        </IconButton>
      ) : null}
      {description ? <span className="ui-toast__description">{description}</span> : null}
    </div>
  );
}

interface ToastContextValue {
  show: (options: ToastOptions) => string;
  dismiss: (id: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

/**
 * 通知区域。两端各自在根组件挂一次。
 *
 * 通知只用于「已经发生的事实」的短反馈；阻塞性错误、需要用户决策的冲突和不可恢复失败
 * 必须留在页面里（ErrorState / Dialog），不能只用一条会自动消失的 toast 表达。
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastRecord[]>([]);
  const counter = useRef(0);
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: string) => {
    const timer = timers.current.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const show = useCallback(
    (options: ToastOptions) => {
      counter.current += 1;
      const id = `toast-${counter.current}`;
      const tone = options.tone ?? 'info';
      // 危险级默认不自动消失：用户很可能正在读它，自动收起会让失败原因消失得无迹可寻。
      const timeoutMs = options.timeoutMs ?? (tone === 'danger' ? 0 : 6000);
      setToasts((current) => [...current, { ...options, tone, id }]);
      if (timeoutMs > 0) {
        timers.current.set(
          id,
          setTimeout(() => dismiss(id), timeoutMs)
        );
      }
      return id;
    },
    [dismiss]
  );

  const value = useMemo<ToastContextValue>(() => ({ show, dismiss }), [show, dismiss]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="ui-toast-region" role="region" aria-label="通知">
        {toasts.map((toast) => (
          <Toast
            key={toast.id}
            title={toast.title}
            description={toast.description}
            tone={toast.tone}
            onDismiss={() => dismiss(toast.id)}
          />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const value = useContext(ToastContext);
  if (!value) throw new Error('ToastProvider 缺失');
  return value;
}
