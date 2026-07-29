import type { SVGProps } from 'react';

export type IconName =
  | 'home'
  | 'works'
  | 'creators'
  | 'files'
  | 'favorite'
  | 'search'
  | 'menu'
  | 'scan'
  | 'diagnostics'
  | 'security'
  | 'rules'
  | 'governance'
  | 'external';

const PATHS: Record<IconName, readonly string[]> = {
  home: ['M3 10.5 12 3l9 7.5', 'M5 9.5V21h14V9.5', 'M9 21v-7h6v7'],
  works: ['M4 4h7v7H4z', 'M13 4h7v7h-7z', 'M4 13h7v7H4z', 'M13 13h7v7h-7z'],
  creators: [
    'M16 20v-1.5a4.5 4.5 0 0 0-9 0V20',
    'M11.5 12A4 4 0 1 0 11.5 4a4 4 0 0 0 0 8Z',
    'M17 8h4',
    'M19 6v4'
  ],
  files: ['M3 6.5h7l2 2h9V20H3z', 'M3 6.5V4h7l2 2h9v2.5'],
  favorite: ['M12 20.5 4.8 13.4A5 5 0 0 1 12 6.5a5 5 0 0 1 7.2 6.9Z'],
  search: ['M10.5 18a7.5 7.5 0 1 1 0-15 7.5 7.5 0 0 1 0 15Z', 'm16 16 5 5'],
  menu: ['M4 7h16', 'M4 12h16', 'M4 17h16'],
  scan: ['M4 8V4h4', 'M16 4h4v4', 'M20 16v4h-4', 'M8 20H4v-4', 'M7 12h10'],
  diagnostics: ['M3 18h3l3-10 4 12 3-8 2 6h3'],
  security: ['M12 3 20 6v6c0 5-3.3 7.7-8 9-4.7-1.3-8-4-8-9V6Z', 'm9 12 2 2 4-5'],
  rules: ['M6 4h12', 'M6 10h12', 'M6 16h12', 'M4 4h.01', 'M4 10h.01', 'M4 16h.01'],
  governance: ['M12 3v4', 'M5 8h14', 'M6 8v8', 'M18 8v8', 'M3 20h18', 'M4 16h4', 'M16 16h4'],
  external: ['M14 4h6v6', 'm20 4-9 9', 'M18 13v7H4V6h7']
};

export interface IconProps extends Omit<SVGProps<SVGSVGElement>, 'children'> {
  name: IconName;
  label?: string;
}

/** 两个入口共用的单线图标。业务平台图标仍由规则 presentation 提供，不在这里硬编码。 */
export function Icon({ name, label, ...props }: IconProps) {
  return (
    <svg
      {...props}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="square"
      strokeLinejoin="miter"
      aria-hidden={label === undefined ? 'true' : undefined}
      aria-label={label}
      focusable="false"
    >
      {PATHS[name].map((path) => (
        <path d={path} key={path} />
      ))}
    </svg>
  );
}
