import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./app/**/*.{ts,tsx}",
    "./components/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // Calming sky-blue palette
        sky: {
          50:  "#f0f9ff",
          100: "#e0f2fe",
          200: "#bae6fd",
          300: "#7dd3fc",
          400: "#38bdf8",
          500: "#0ea5e9",
          600: "#0284c7",
          700: "#0369a1",
          800: "#075985",
          900: "#0c4a6e",
        },
      },
      backgroundImage: {
        "aurora":
          "radial-gradient(1200px 600px at 10% -10%, rgba(125,211,252,0.55), transparent 60%)," +
          "radial-gradient(900px 500px at 110% 10%, rgba(186,230,253,0.55), transparent 60%)," +
          "radial-gradient(800px 500px at 50% 120%, rgba(224,242,254,0.7), transparent 60%)",
      },
      boxShadow: {
        glass: "0 8px 32px 0 rgba(2, 132, 199, 0.18)",
        "glass-sm": "0 4px 16px 0 rgba(2, 132, 199, 0.12)",
      },
      animation: {
        "fade-in": "fadeIn 400ms ease-out",
        "float": "float 6s ease-in-out infinite",
      },
      keyframes: {
        fadeIn: {
          "0%": { opacity: "0", transform: "translateY(8px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
        float: {
          "0%,100%": { transform: "translateY(0)" },
          "50%": { transform: "translateY(-6px)" },
        },
      },
    },
  },
  plugins: [],
};

export default config;
