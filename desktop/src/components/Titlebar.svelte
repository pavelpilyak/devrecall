<script lang="ts">
  import Icon from "./ui/Icon.svelte";
  import Kbd from "./ui/Kbd.svelte";
  import type { SyncStatus } from "./ui/tokens";

  let {
    syncStatus = "ok",
    syncLabel = "Sync",
    syncAgo = "",
    syncing = false,
    onSync,
    onCmdK,
  } = $props<{
    syncStatus?: SyncStatus;
    syncLabel?: string;
    syncAgo?: string;
    syncing?: boolean;
    onSync?: () => void;
    onCmdK?: () => void;
  }>();
</script>

<div class="titlebar" data-tauri-drag-region>
  <div class="wordmark" aria-label="DevRecall">
    <svg width="18" height="18" viewBox="8 8 24 24" aria-hidden="true">
      <path d="M10 12 L22 20 L10 28" fill="none" stroke="#e7ecf2" stroke-width="2.2" />
      <circle cx="28" cy="20" r="3.2" fill="#7cf0a8" />
    </svg>
  </div>

  <div class="spacer" data-tauri-drag-region></div>

  <button
    type="button"
    class="sync-btn"
    class:is-syncing={syncing}
    class:is-error={syncStatus === "error"}
    class:is-warn={syncStatus === "warn"}
    title={syncAgo ? `${syncLabel} · last synced ${syncAgo} ago` : syncLabel}
    disabled={syncing}
    onclick={() => onSync?.()}
  >
    <Icon name="refresh-cw" size={12} />
    <span>{syncLabel}</span>
    {#if syncAgo}<span class="sync-ago">{syncAgo}</span>{/if}
  </button>

  <button type="button" class="cmdk" onclick={() => onCmdK?.()}>
    <Icon name="search" size={12} />
    <span>Search or run</span>
    <Kbd>{#snippet children()}⌘{/snippet}</Kbd>
    <Kbd>{#snippet children()}K{/snippet}</Kbd>
  </button>
</div>

<style>
  .titlebar {
    height: 38px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 14px;
    background: rgba(13, 15, 18, 0.85);
    backdrop-filter: blur(16px);
    -webkit-backdrop-filter: blur(16px);
    border-bottom: 1px solid var(--border);
    user-select: none;
    position: relative;
    z-index: 10;
  }
  .wordmark {
    display: flex;
    align-items: center;
  }
  .spacer { flex: 1; }
  .sync-btn {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    background: var(--ink-3);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 4px 10px 4px 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-2);
    cursor: pointer;
    transition: background var(--dur-1) var(--ease-std), color var(--dur-1) var(--ease-std);
  }
  .sync-btn:hover:not(:disabled) { background: var(--ink-4); color: var(--fg-1); }
  .sync-btn:disabled { cursor: default; opacity: 0.8; }
  .sync-btn.is-warn { color: var(--fg-3); }
  .sync-btn.is-error { color: var(--danger); border-color: rgba(255, 107, 107, 0.3); }
  .sync-ago {
    color: var(--fg-4);
    font-size: 10px;
    border-left: 1px solid var(--hairline);
    padding-left: 8px;
    margin-left: 2px;
  }
  .sync-btn.is-syncing :global(svg:first-of-type) {
    animation: dr-spin 900ms linear infinite;
  }
  @keyframes dr-spin {
    to { transform: rotate(360deg); }
  }
  .cmdk {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    background: var(--ink-3);
    border: 1px solid var(--border);
    border-radius: var(--r-2);
    padding: 4px 8px;
    color: var(--fg-3);
    font-family: var(--font-sans);
    font-size: 12px;
    cursor: pointer;
  }
  .cmdk:hover { background: var(--ink-4); color: var(--fg-1); }
</style>
