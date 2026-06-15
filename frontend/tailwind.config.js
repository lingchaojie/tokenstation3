/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - Orange 橙色系 (LINX2 fox brand)
        primary: {
          50: '#fff7ed',
          100: '#ffedd5',
          200: '#fed7aa',
          300: '#fdba74',
          400: '#fb923c',
          500: '#f97316',
          600: '#ea580c',
          700: '#c2410c',
          800: '#9a3412',
          900: '#7c2d12',
          950: '#431407'
        },
        // 辅助色 - 深蓝灰
        accent: {
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617'
        },
        // 深色模式背景
        dark: {
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617'
        },
        linear: {
          canvas: 'rgb(var(--linear-canvas) / <alpha-value>)',
          surface: {
            1: 'rgb(var(--linear-surface-1) / <alpha-value>)',
            2: 'rgb(var(--linear-surface-2) / <alpha-value>)',
            3: 'rgb(var(--linear-surface-3) / <alpha-value>)',
            4: 'rgb(var(--linear-surface-4) / <alpha-value>)'
          },
          hairline: 'rgb(var(--linear-hairline) / <alpha-value>)',
          'hairline-strong': 'rgb(var(--linear-hairline-strong) / <alpha-value>)',
          ink: {
            DEFAULT: 'rgb(var(--linear-ink) / <alpha-value>)',
            muted: 'rgb(var(--linear-ink-muted) / <alpha-value>)',
            subtle: 'rgb(var(--linear-ink-subtle) / <alpha-value>)',
            tertiary: 'rgb(var(--linear-ink-tertiary) / <alpha-value>)'
          }
        }
      },
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        glass: '0 1px 0 rgba(255, 255, 255, 0.04) inset, 0 20px 80px rgba(0, 0, 0, 0.28)',
        'glass-sm': '0 1px 0 rgba(255, 255, 255, 0.04) inset, 0 12px 40px rgba(0, 0, 0, 0.22)',
        glow: '0 0 0 1px rgba(249, 115, 22, 0.28)',
        'glow-lg': '0 0 0 1px rgba(249, 115, 22, 0.34)',
        card: '0 1px 0 rgba(255, 255, 255, 0.04) inset',
        'card-hover': '0 1px 0 rgba(255, 255, 255, 0.06) inset',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.08)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #f97316 0%, #ea580c 100%)',
        'gradient-dark': 'linear-gradient(135deg, #1e293b 0%, #0f172a 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(249, 115, 22, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(251, 146, 60, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(249, 115, 22, 0.08) 0px, transparent 50%)'
      },
      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(249, 115, 22, 0.25)' },
          '100%': { boxShadow: '0 0 30px rgba(249, 115, 22, 0.4)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}
