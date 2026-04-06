<script lang="ts">
  import { isPro } from "../lib/license";
  import type { Feature } from "../lib/license";
  import { featureLabel } from "../lib/license";

  interface Props {
    feature: Feature;
    children?: import("svelte").Snippet;
  }

  let { feature, children }: Props = $props();

  let showActivate = $state(false);
  let licenseKey = $state("");
  let activating = $state(false);
  let activateError = $state("");
  let activateSuccess = $state("");

  async function handleActivate() {
    if (!licenseKey.trim()) return;
    activating = true;
    activateError = "";
    activateSuccess = "";
    try {
      const { api } = await import("../lib/api");
      const result = await api.activate(licenseKey.trim());
      activateSuccess = result.message;
      // Refresh license info in stores.
      const { checkConnection } = await import("../lib/stores");
      await checkConnection();
    } catch (e) {
      activateError = e instanceof Error ? e.message : "Activation failed";
    } finally {
      activating = false;
    }
  }

  function openPricing() {
    window.open("https://devrecall.dev/pricing", "_blank");
  }
</script>

{#if $isPro}
  {#if children}
    {@render children()}
  {/if}
{:else}
  <div class="flex flex-col items-center justify-center h-full p-8">
    <div class="max-w-sm text-center space-y-4">
      <div class="text-3xl">&#128274;</div>
      <h3 class="text-lg font-semibold">
        {featureLabel(feature)} requires Pro
      </h3>

      <div class="text-sm text-zinc-600 dark:text-zinc-400 text-left space-y-1">
        <p class="font-medium">Pro plan includes:</p>
        <ul class="list-disc list-inside space-y-0.5">
          <li>All integrations (Slack, Calendar, GitHub, Jira, Linear)</li>
          <li>AI-powered standups &amp; chat</li>
          <li>Brag docs &amp; perf reviews</li>
          <li>Cloud backup</li>
        </ul>
      </div>

      <p class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        $99 one-time purchase (1 device)
      </p>

      <div class="flex gap-2 justify-center">
        <button
          onclick={openPricing}
          class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
                 hover:bg-devrecall-700 transition-colors"
        >
          View Pricing
        </button>
        <button
          onclick={() => showActivate = !showActivate}
          class="px-4 py-2 text-sm font-medium rounded-lg border border-zinc-300
                 dark:border-zinc-600 text-zinc-700 dark:text-zinc-300
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
        >
          Enter License Key
        </button>
      </div>

      {#if showActivate}
        <div class="mt-3 space-y-2">
          <div class="flex gap-2">
            <input
              type="text"
              bind:value={licenseKey}
              placeholder="DR-PRO-XXXX-XXXX-XXXX"
              class="flex-1 px-3 py-2 text-sm rounded-lg border border-zinc-300
                     dark:border-zinc-600 bg-white dark:bg-zinc-800
                     focus:outline-none focus:ring-2 focus:ring-devrecall-500"
              onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") handleActivate(); }}
            />
            <button
              onclick={handleActivate}
              disabled={activating || !licenseKey.trim()}
              class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
                     hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
            >
              {activating ? "..." : "Activate"}
            </button>
          </div>
          {#if activateError}
            <p class="text-xs text-red-500">{activateError}</p>
          {/if}
          {#if activateSuccess}
            <p class="text-xs text-green-600 dark:text-green-400">{activateSuccess}</p>
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/if}
