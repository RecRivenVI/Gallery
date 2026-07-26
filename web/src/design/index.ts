/*
 * 设计系统统一入口。
 *
 * 画廊与管理两条工作线只从这里导入；直接 import '../design/primitives' 或复制某个 ui- 类名
 * 都会绕开这里的样式装载顺序。
 *
 * 这里的 import 顺序就是最终 CSS 的层叠顺序，不要调整：
 *   tokens → reset → primitives（由 primitives.tsx 自身引入）
 */

import './tokens.css';
import './reset.css';

export * from './primitives';
