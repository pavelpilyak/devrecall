<script lang="ts">
  import type { SyncStatus } from "./tokens";

  let { status = "ok" }: { status?: SyncStatus } = $props();

  const colorFor = (s: SyncStatus) =>
    s === "syncing" ? "var(--accent)"
    : s === "warn" ? "var(--warn)"
    : s === "error" ? "var(--danger)"
    : "var(--ok)";
</script>

<span
  class="sync-dot"
  class:pulsing={status === "syncing"}
  style="background:{colorFor(status)}"
></span>

<style>
  .sync-dot {
    display: inline-block;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .pulsing {
    animation: drPulse 1.5s ease-in-out infinite;
  }
  @keyframes drPulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
  }
</style>
