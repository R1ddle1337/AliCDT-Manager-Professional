/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js}'],
  theme: {
    extend: {
      colors: {
        bg: '#f4f7fb',
        background: '#f4f7fb',
        surface: '#ffffff',
        'surface-hover': '#f8fafc',
        card: '#ffffff',
        border: '#e5eaf1',
        accent: '#2563eb',
        'accent-light': '#3b82f6',
        success: '#16a34a',
        warning: '#d97706',
        'warning-dark': '#b45309',
        danger: '#dc2626',
        muted: '#64748b',
        text: '#172033',
        'text-muted': '#64748b',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', 'SF Pro Display', 'Inter', 'sans-serif'],
        mono: ['SF Mono', 'JetBrains Mono', 'monospace'],
      },
      borderRadius: {
        xl: '1rem',
        '2xl': '1.25rem',
      },
      boxShadow: {
        card: '0 0 0 1px rgba(255,255,255,0.05), 0 4px 24px rgba(0,0,0,0.4)',
        glow: '0 0 20px rgba(99,102,241,0.3)',
      },
    },
  },
  plugins: [],
}
