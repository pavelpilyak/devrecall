/** @type {import('tailwindcss').Config} */
export default {
  darkMode: "class",
  content: ["./src/**/*.{html,svelte,js,ts}"],
  theme: {
    extend: {
      colors: {
        mint: {
          50: "#e6fbef",
          100: "#c8f6d8",
          200: "#9ef0bd",
          300: "#7cf0a8",
          400: "#4cdb87",
          500: "#22c56a",
          600: "#179b52",
          700: "#11733d",
          800: "#0c4d29",
        },
        accent: "var(--accent)",
        "accent-ink": "var(--accent-ink)",
        ink: {
          0: "var(--ink-0)",
          1: "var(--ink-1)",
          2: "var(--ink-2)",
          3: "var(--ink-3)",
          4: "var(--ink-4)",
          5: "var(--ink-5)",
          6: "var(--ink-6)",
        },
        fg: {
          1: "var(--fg-1)",
          2: "var(--fg-2)",
          3: "var(--fg-3)",
          4: "var(--fg-4)",
          mute: "var(--fg-mute)",
        },
        // Legacy alias kept so pre-existing `devrecall-600` classes resolve to mint.
        devrecall: {
          50: "#e6fbef",
          500: "#4cdb87",
          600: "#7cf0a8",
          700: "#22c56a",
          900: "#0c4d29",
        },
      },
      fontFamily: {
        sans: ["Inter", "-apple-system", "BlinkMacSystemFont", "Segoe UI", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "ui-monospace", "SF Mono", "Menlo", "Consolas", "monospace"],
        serif: ["Source Serif 4", "Iowan Old Style", "Georgia", "serif"],
      },
      borderRadius: {
        DEFAULT: "5px",
        xs: "3px",
        sm: "5px",
        md: "7px",
        lg: "10px",
      },
    },
  },
  plugins: [],
};
