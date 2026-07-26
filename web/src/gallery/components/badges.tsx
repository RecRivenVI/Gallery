/*
 * 规则派生角标。
 *
 * 角标是**Source-derived 展示事实**：出现条件与配色都由规则裁决，随快照下发。客户端只按
 * 服务端给的 position 与顺序渲染，既不推导“什么时候该有角标”，也不自己挑颜色。
 *
 * 高对比主题是唯一例外：规则作者挑的颜色是按普通浅/深色主题选的，无法保证 7:1 对比度，
 * 因此此时退回设计系统的 Badge（token 配色）。这不是“客户端自行推导颜色”，而是可访问性
 * 兜底——角标的**语义**仍然完全来自服务端。
 */

import { Badge } from '../../design';
import { useTheme } from '../../shared/theme';
import { badgeStyle, badgesAt, type RuleBadge } from '../contracts';

export interface RuleBadgesProps {
  badges: readonly RuleBadge[] | undefined;
  position: RuleBadge['position'];
  className?: string;
}

export function RuleBadges({ badges, position, className }: RuleBadgesProps) {
  const { resolvedTheme } = useTheme();
  const items = badgesAt(badges, position);
  if (items.length === 0) return null;
  return (
    <span className={className === undefined ? 'gal-badges' : `gal-badges ${className}`}>
      {items.map((badge) => {
        const style = badgeStyle(badge, resolvedTheme);
        if (!style) {
          return (
            <Badge key={badge.id} tone="neutral">
              {badge.label}
            </Badge>
          );
        }
        return (
          <span key={badge.id} className="gal-badge" style={style}>
            {badge.label}
          </span>
        );
      })}
    </span>
  );
}
