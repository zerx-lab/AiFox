// LLMFox icons - small inline SVGs

const Icon = ({ name, size = 14, className = '', style = {} }) => {
  const paths = {
    play: 'M5 3l11 7-11 7V3z',
    pause: 'M5 3h4v14H5zM12 3h4v14h-4z',
    stop: 'M4 4h12v12H4z',
    'step-over': 'M3 10h11M14 10l-4-4M14 10l-4 4',
    'step-into': 'M10 3v10M10 13l-4-4M10 13l4-4',
    'step-out': 'M10 17V7M10 7l-4 4M10 7l4 4',
    restart: 'M4 10a6 6 0 1 1 1.7 4.2M4 14v-4h4',
    chevron: 'M6 4l5 6-5 6',
    chevdown: 'M4 7l6 5 6-5',
    dot: 'M10 10m-3 0a3 3 0 1 0 6 0a3 3 0 1 0 -6 0',
    user: 'M10 10a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM4 17c0-3 3-5 6-5s6 2 6 5',
    bot: 'M5 6h10v8H5zM7 9h.01M13 9h.01M3 10h2M15 10h2M10 3v3',
    tool: 'M14.7 6.3a4 4 0 0 1-5.4 5.4L4 17l-1-1 5.3-5.3a4 4 0 0 1 5.4-5.4l-2 2 1.4 1.4 2-2z',
    search: 'M9 3a6 6 0 1 0 4 10.5L17 17M9 3a6 6 0 0 1 6 6',
    breakpoint: 'M10 10m-5 0a5 5 0 1 0 10 0a5 5 0 1 0 -10 0',
    moon: 'M16 11a6 6 0 0 1-8.5-7.5A7 7 0 1 0 16 11z',
    sun: 'M10 4V2M10 18v-2M4 10H2M18 10h-2M5 5l-1.4-1.4M16.4 16.4L15 15M5 15l-1.4 1.4M16.4 3.6L15 5M10 6a4 4 0 1 0 0 8a4 4 0 0 0 0-8z',
    file: 'M5 2h6l4 4v12H5z M11 2v4h4',
    code: 'M7 6l-4 4 4 4M13 6l4 4-4 4',
    diff: 'M6 3v10a2 2 0 0 0 2 2h6M14 17v-10a2 2 0 0 0-2-2H6M3 14l3 3 3-3M17 6l-3-3-3 3',
    list: 'M3 5h14M3 10h14M3 15h14',
    'stack': 'M3 4l7-2 7 2-7 2zM3 10l7 2 7-2M3 14l7 2 7-2',
    flame: 'M10 17a5 5 0 0 0 5-5c0-2-1-4-3-6 0 2-1.5 3-3 3 0-3-2-5-3-6-1 4-4 4-4 9a5 5 0 0 0 5 5z',
    settings: 'M10 6a4 4 0 1 0 0 8a4 4 0 0 0 0-8z M10 1v2M10 17v2M3.5 3.5l1.5 1.5M15 15l1.5 1.5M1 10h2M17 10h2M3.5 16.5l1.5-1.5M15 5l1.5-1.5',
    refresh: 'M3 10a7 7 0 0 1 12-5l2 2M17 4v4h-4M17 10a7 7 0 0 1-12 5l-2-2M3 16v-4h4',
    bug: 'M10 6a3 3 0 0 0-3 3v3a3 3 0 0 0 6 0V9a3 3 0 0 0-3-3zM7 6V4M13 6V4M3 9h4M13 9h4M3 13h4M13 13h4M10 15v3',
    db: 'M4 5c0-1.5 3-3 6-3s6 1.5 6 3M4 5v10c0 1.5 3 3 6 3s6-1.5 6-3V5M4 5c0 1.5 3 3 6 3s6-1.5 6-3M4 10c0 1.5 3 3 6 3s6-1.5 6-3',
    plus: 'M10 4v12M4 10h12',
    x: 'M5 5l10 10M15 5L5 15',
    filter: 'M3 4h14l-5 7v5l-4 2v-7z',
    branch: 'M6 3v14M14 3v6a3 3 0 0 1-3 3H6M6 5a2 2 0 1 0 0-4a2 2 0 0 0 0 4zM6 19a2 2 0 1 0 0-4a2 2 0 0 0 0 4zM14 5a2 2 0 1 0 0-4a2 2 0 0 0 0 4z',
    spark: 'M10 2v6M10 12v6M2 10h6M12 10h6M5 5l3 3M12 12l3 3M5 15l3-3M12 8l3-3',
  };
  const d = paths[name] || paths.dot;
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size} height={size}
      viewBox="0 0 20 20"
      fill="none" stroke="currentColor" strokeWidth="1.5"
      strokeLinecap="round" strokeLinejoin="round"
      className={className} style={style}
    >
      <path d={d} />
    </svg>
  );
};

window.Icon = Icon;
