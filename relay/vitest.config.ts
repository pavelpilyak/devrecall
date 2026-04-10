import { defineWorkersConfig } from "@cloudflare/vitest-pool-workers/config";

export default defineWorkersConfig({
  test: {
    poolOptions: {
      workers: {
        wrangler: { configPath: "./wrangler.toml" },
        miniflare: {
          bindings: {
            LICENSE_SIGNING_KEY: `[REDACTED-PRIVATE-KEY]`,
            LEMON_SQUEEZY_WEBHOOK_SECRET: "test-webhook-secret",
          },
        },
      },
    },
  },
});
