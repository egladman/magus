export type DashboardMode = "plan";

export type SurfaceNavigation = {
  pageId: string;
  dashboardMode?: DashboardMode;
};

const eventName = "console:open-surface";

export function openSurface(detail: SurfaceNavigation): void {
  window.dispatchEvent(new CustomEvent<SurfaceNavigation>(eventName, { detail }));
}

export function surfaceNavigation(event: Event): SurfaceNavigation | null {
  if (!(event instanceof CustomEvent)) return null;
  const detail = event.detail;
  if (!detail || typeof detail !== "object") return null;
  const { pageId, dashboardMode } = detail as Partial<SurfaceNavigation>;
  if (typeof pageId !== "string") return null;
  if (dashboardMode !== undefined && dashboardMode !== "plan") return null;
  return { pageId, dashboardMode };
}

export { eventName as surfaceNavigationEvent };
