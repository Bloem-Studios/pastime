/** The product mark: three stacked spines of different heights, a shelf of stories. */
export function BrandMark({ size = 32 }: { size?: number }) {
  return <svg className="brand-svg" width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" focusable="false">
    <rect width="32" height="32" rx="9" fill="currentColor" />
    <rect x="8" y="9" width="4" height="15" rx="1.4" fill="var(--brand-ink, #141417)" />
    <rect x="14" y="6.5" width="4" height="17.5" rx="1.4" fill="var(--brand-ink, #141417)" opacity=".82" />
    <rect x="20.2" y="9.8" width="4" height="14.4" rx="1.4" fill="var(--brand-ink, #141417)" opacity=".64" transform="rotate(9 22.2 17)" />
  </svg>;
}
