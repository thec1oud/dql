import { defineConfig } from "vitest/config";

// Every package's hand-written code is held to 100% coverage, as the Go
// modules are (scripts/coverage.sh).
export default defineConfig({
  test: {
    include: ["*/test/**/*.test.ts"],
    coverage: {
      provider: "v8",
      include: ["*/src/**/*.ts"],
      reporter: ["text"],
      thresholds: { 100: true },
    },
  },
});
