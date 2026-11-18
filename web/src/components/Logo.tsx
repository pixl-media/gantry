// The gantry mark from docs/logo, cropped to the drawing. The frame takes the
// surrounding text color, so it fits the app bar and both color schemes; the
// container stays crane yellow, and its ribs are holes that show the
// background. Decorative: the name always stands next to it.
export function Logo({ height = 28 }: { height?: number }) {
  return (
    <svg
      viewBox="6 9 52 47"
      height={height}
      width={(height * 52) / 47}
      aria-hidden="true"
      focusable="false"
      style={{ display: 'block', flexShrink: 0 }}
    >
      <g fill="currentColor">
        <rect x="6" y="9" width="52" height="7" rx="2" />
        <rect x="10" y="9" width="7" height="47" rx="2" />
        <rect x="47" y="9" width="7" height="47" rx="2" />
        <rect x="30.5" y="16" width="3" height="12" />
      </g>
      <path
        fill="#f2a33a"
        fillRule="evenodd"
        d="M23.5 28H40.5A2.5 2.5 0 0 1 43 30.5V41.5A2.5 2.5 0 0 1 40.5 44H23.5A2.5 2.5 0 0 1 21 41.5V30.5A2.5 2.5 0 0 1 23.5 28ZM26.5 31.5A1 1 0 0 1 28.5 31.5V40.5A1 1 0 0 1 26.5 40.5ZM31 31.5A1 1 0 0 1 33 31.5V40.5A1 1 0 0 1 31 40.5ZM35.5 31.5A1 1 0 0 1 37.5 31.5V40.5A1 1 0 0 1 35.5 40.5Z"
      />
    </svg>
  );
}
