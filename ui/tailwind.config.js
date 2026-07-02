import cloistrPreset from '@cloistr/tailwind-config';

/** @type {import('tailwindcss').Config} */
export default {
  presets: [cloistrPreset],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  plugins: [],
}
