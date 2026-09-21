/**
 * The widget manifest.
 *
 * Importing a widget module is what registers it — each one calls
 * `registerWidget` at module scope. This file is the single place that import
 * happens, so the production rendering path and the registry invariant tests
 * register exactly the same set. Two separate lists would drift, and a drifted
 * list is invisible: the invariants would keep passing while checking fewer
 * widgets than the codebase contains.
 *
 * Every module under this directory must be listed here.
 * `registry_test.ts` reads this file against a directory listing and fails when
 * one is missing, so the list cannot silently fall behind.
 *
 * Side-effect imports only — this module intentionally exports nothing.
 */
import "./ActiveAlertsWidget.tsx";
import "./AuditActivityWidget.tsx";
import "./CertsExpiringWidget.tsx";
import "./ClusterHealthWidget.tsx";
import "./ClusterStatusWidget.tsx";
import "./CpuTileWidget.tsx";
import "./DiagnosticsSummaryWidget.tsx";
import "./EsoHealthWidget.tsx";
import "./GatewayRoutesWidget.tsx";
import "./GitOpsAppHealthWidget.tsx";
import "./GitOpsRecentSyncsWidget.tsx";
import "./HPAStatusWidget.tsx";
import "./HubbleFlowsWidget.tsx";
import "./MemoryTileWidget.tsx";
import "./MeshGoldenSignalsWidget.tsx";
import "./MtlsCoverageWidget.tsx";
import "./NetworkTileWidget.tsx";
import "./NodeConditionsWidget.tsx";
import "./NodesWidget.tsx";
import "./NotificationsFeedWidget.tsx";
import "./PDBRiskWidget.tsx";
import "./PendingPodsWidget.tsx";
import "./PinnedResourcesWidget.tsx";
import "./PodRestartsWidget.tsx";
import "./PodStatusWidget.tsx";
import "./PodsTileWidget.tsx";
import "./PolicyComplianceWidget.tsx";
import "./PolicyViolationsWidget.tsx";
import "./QuotaPressureWidget.tsx";
import "./RecentEventsWidget.tsx";
import "./ResourceUtilizationWidget.tsx";
import "./SavedViewsWidget.tsx";
import "./SnapshotHealthWidget.tsx";
import "./StorageCapacityWidget.tsx";
import "./TopConsumersWidget.tsx";
import "./VeleroBackupsWidget.tsx";
import "./VulnerabilitySeverityWidget.tsx";
import "./WorkloadHealthWidget.tsx";
