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
