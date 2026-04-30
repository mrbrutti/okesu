import type { Config } from 'tailwindcss';

// Brand tokens are an exact mirror of web/tailwind.config.ts. Keep
// them in sync manually for v1; a future workspace package can host
// the shared config if drift becomes a problem.
export default {
  content: ['./src/**/*.{astro,html,ts,tsx,mdx}'],
  theme: {
    extend: {
      colors: {
        bg: '#fafafa',
        panel: '#ffffff',
        border: '#e5e7eb',
        ink: {
          DEFAULT: '#0f172a',
          dim: '#64748b',
          mute: '#94a3b8',
        },
        brand: {
          50:  '#f5f3ff',
          100: '#ede9fe',
          500: '#7c3aed',
          600: '#6d28d9',
          700: '#5b21b6',
        },
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'system-ui', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px rgba(15, 23, 42, 0.04), 0 0 0 1px rgba(15, 23, 42, 0.06)',
      },
      maxWidth: {
        'content': '720px',
        'page': '1120px',
      },
    },
  },
  plugins: [],
} satisfies Config;
