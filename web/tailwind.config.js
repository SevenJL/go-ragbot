/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{js,jsx}"],
  theme: {
    extend: {
      colors: {
        ink: "#13110f",
        ember: "#dc6f38",
        ochre: "#e4b15f",
        parchment: "#f4efe6",
        slate: "#334155",
        fog: "#d7d1c4"
      },
      boxShadow: {
        panel: "0 18px 60px rgba(19, 17, 15, 0.18)"
      },
      fontFamily: {
        display: ["Space Grotesk", "Noto Sans SC", "sans-serif"],
        body: ["Manrope", "Noto Sans SC", "sans-serif"]
      },
      backgroundImage: {
        grid: "linear-gradient(rgba(19,17,15,0.08) 1px, transparent 1px), linear-gradient(90deg, rgba(19,17,15,0.08) 1px, transparent 1px)"
      }
    }
  },
  plugins: []
};
