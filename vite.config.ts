import { defineConfig } from "vite";
import dts from "vite-plugin-dts";

export default defineConfig({
  build: {
    lib: {
      entry: {
        index: "src/index.ts",
        node: "src/node.ts"
      },
      formats: ["es"]
    },
    rollupOptions: {
      external: [
        "node:child_process",
        "node:fs/promises",
        "node:os",
        "node:path"
      ]
    },
    sourcemap: true,
    minify: false,
    target: "es2022"
  },
  plugins: [
    dts({
      include: ["src"]
    })
  ],
  test: {
    globals: true,
    coverage: {
      reporter: ["text", "json-summary"]
    }
  }
});
