import { defineConfig } from "vite";

// The probe page is embedded into the fixture app (../dist) and served on
// mygo://localhost. No dev server: the fixture tests the packaged path.
export default defineConfig({
  build: {
    outDir: "../dist",
    emptyOutDir: false,
    target: "es2022",
  },
});
