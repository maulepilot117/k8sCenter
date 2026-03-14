/** SVG icons for Kubernetes resource types. Sized at 20x20 by default. */

interface ResourceIconProps {
  kind: string;
  class?: string;
  size?: number;
}

export function ResourceIcon(
  { kind, class: className, size = 20 }: ResourceIconProps,
) {
  const icon = icons[kind] ?? icons.default;
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      class={className}
    >
      {icon}
    </svg>
  );
}

const icons: Record<string, ReturnType<typeof SVGContent>> = {
  dashboard: (
    <path d="M3 5h14M3 5v10a2 2 0 002 2h10a2 2 0 002-2V5M3 5l7 5 7-5" />
  ),
  nodes: (
    <>
      <rect x="3" y="3" width="5" height="5" rx="1" />
      <rect x="12" y="3" width="5" height="5" rx="1" />
      <rect x="7.5" y="12" width="5" height="5" rx="1" />
      <path d="M5.5 8v2.5a1 1 0 001 1h7a1 1 0 001-1V8M10 11.5V12" />
    </>
  ),
  namespaces: (
    <>
      <rect x="2" y="2" width="16" height="16" rx="2" />
      <path d="M2 7h16M7 7v11" />
    </>
  ),
  events: (
    <>
      <path d="M13 2L3 14h7l-1 6 10-12h-7l1-6" />
    </>
  ),
  deployments: (
    <>
      <circle cx="10" cy="10" r="7" />
      <path d="M10 6v4l2.5 2.5" />
    </>
  ),
  statefulsets: (
    <>
      <rect x="3" y="3" width="14" height="4" rx="1" />
      <rect x="3" y="9" width="14" height="4" rx="1" />
      <path d="M6 15h8" />
    </>
  ),
  daemonsets: (
    <>
      <circle cx="10" cy="10" r="7" />
      <circle cx="10" cy="10" r="3" />
    </>
  ),
  pods: (
    <>
      <ellipse cx="10" cy="10" rx="7" ry="5" />
      <path d="M3 10c0 2.76 3.13 5 7 5s7-2.24 7-5" />
      <path d="M10 5v10" />
    </>
  ),
  jobs: (
    <>
      <path d="M4 4l6 6M16 4l-6 6M10 10v6" />
      <circle cx="10" cy="10" r="2" />
    </>
  ),
  cronjobs: (
    <>
      <circle cx="10" cy="10" r="7" />
      <path d="M10 6v4l3 3" />
      <path d="M15 3l2 2M3 3l2 2" />
    </>
  ),
  services: (
    <>
      <path d="M4 10h12" />
      <circle cx="4" cy="10" r="2" />
      <circle cx="16" cy="10" r="2" />
      <circle cx="10" cy="5" r="2" />
      <circle cx="10" cy="15" r="2" />
      <path d="M10 7v6" />
    </>
  ),
  ingresses: (
    <>
      <path d="M2 10h4M14 10h4M10 2v4M10 14v4" />
      <rect x="6" y="6" width="8" height="8" rx="2" />
    </>
  ),
  networkpolicies: (
    <>
      <rect x="3" y="3" width="14" height="14" rx="2" />
      <path d="M3 10h14M10 3v14" />
    </>
  ),
  pvcs: (
    <>
      <path d="M4 4h12v10a2 2 0 01-2 2H6a2 2 0 01-2-2V4z" />
      <path d="M4 4l3-2h6l3 2" />
      <path d="M8 9h4" />
    </>
  ),
  configmaps: (
    <>
      <rect x="3" y="3" width="14" height="14" rx="2" />
      <path d="M7 7h6M7 10h6M7 13h4" />
    </>
  ),
  secrets: (
    <>
      <rect x="4" y="8" width="12" height="8" rx="2" />
      <path d="M7 8V6a3 3 0 016 0v2" />
      <circle cx="10" cy="12" r="1.5" />
    </>
  ),
  roles: (
    <>
      <circle cx="10" cy="7" r="4" />
      <path d="M4 17c0-3.31 2.69-6 6-6s6 2.69 6 6" />
    </>
  ),
  clusterroles: (
    <>
      <circle cx="10" cy="7" r="4" />
      <path d="M4 17c0-3.31 2.69-6 6-6s6 2.69 6 6" />
      <path d="M15 4l2-2M3 4l2-2" />
    </>
  ),
  rolebindings: (
    <>
      <circle cx="7" cy="8" r="3" />
      <circle cx="13" cy="8" r="3" />
      <path d="M10 8v6" />
    </>
  ),
  clusterrolebindings: (
    <>
      <circle cx="7" cy="8" r="3" />
      <circle cx="13" cy="8" r="3" />
      <path d="M10 8v6M15 4l2-2M3 4l2-2" />
    </>
  ),
  monitoring: (
    <>
      <path d="M3 16l4-6 3 4 4-8 3 5" />
      <path d="M3 17h14" />
    </>
  ),
  dashboards: (
    <>
      <rect x="3" y="3" width="14" height="14" rx="2" />
      <path d="M3 8h14M8 8v9" />
    </>
  ),
  prometheus: (
    <>
      <circle cx="10" cy="10" r="7" />
      <path d="M10 5v5l3 3" />
    </>
  ),
  storage: (
    <>
      <path d="M4 6c0-1.1 2.69-2 6-2s6 .9 6 2v8c0 1.1-2.69 2-6 2s-6-.9-6-2V6z" />
      <path d="M4 10c0 1.1 2.69 2 6 2s6-.9 6-2" />
    </>
  ),
  networking: (
    <>
      <circle cx="10" cy="4" r="2" />
      <circle cx="4" cy="14" r="2" />
      <circle cx="16" cy="14" r="2" />
      <path d="M10 6v4M6.5 12.5L9 10M13.5 12.5L11 10" />
    </>
  ),
  snapshots: (
    <>
      <rect x="4" y="4" width="12" height="12" rx="2" />
      <path d="M4 8h12M8 4v12" />
      <circle cx="12" cy="12" r="2" />
    </>
  ),
  yaml: (
    <>
      <path d="M4 3h12a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V4a1 1 0 011-1z" />
      <path d="M7 7l3 3-3 3M11 13h3" />
    </>
  ),
  alerts: (
    <path d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6 6 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
  ),
  rules: (
    <>
      <path d="M4 3h12a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V4a1 1 0 011-1z" />
      <path d="M7 8h6M7 11h4" />
    </>
  ),
  settings: (
    <path d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.573-1.066z" />
  ),
  default: (
    <>
      <rect x="3" y="3" width="14" height="14" rx="2" />
      <path d="M10 7v6M7 10h6" />
    </>
  ),
};

// Helper type — just gets JSX elements to work as values
function SVGContent(_props: Record<string, never>) {
  return null;
}
