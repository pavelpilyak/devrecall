<script lang="ts">
  import { onMount } from "svelte";
  import { invoke } from "@tauri-apps/api/core";
  import { api, type SourceStatus } from "../lib/api";
  import { licenseInfo, type LicenseInfo } from "../lib/license";
  import { checkConnection } from "../lib/stores";

  let sources = $state<SourceStatus[]>([]);
  let loading = $state(true);
  let syncing = $state(false);
  let error = $state("");

  // License activation
  let licenseKey = $state("");
  let activating = $state(false);
  let activateError = $state("");
  let activateSuccess = $state("");

  // Hotkey remapping
  let currentHotkey = $state("CmdOrCtrl+Shift+D");
  let recording = $state(false);
  let hotkeyError = $state("");

  // Update check
  let updateAvailable = $state<string | null>(null);
  let checkingUpdate = $state(false);
  let updating = $state(false);
  let updateError = $state("");

  async function loadStatus() {
    loading = true;
    error = "";
    try {
      const resp = await api.status();
      sources = resp.sources;
      // Load current hotkey from Tauri backend.
      try {
        currentHotkey = await invoke<string>("get_hotkey");
      } catch {
        // Fallback to default if command not available.
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load status";
    } finally {
      loading = false;
    }
  }

  async function triggerSync() {
    syncing = true;
    try {
      await api.sync();
      // Reload status after sync.
      await loadStatus();
    } catch (e) {
      error = e instanceof Error ? e.message : "Sync failed";
    } finally {
      syncing = false;
    }
  }

  function formatSyncTime(syncedAt?: string): string {
    if (!syncedAt) return "never synced";
    const d = new Date(syncedAt);
    const diff = Date.now() - d.getTime();
    const mins = Math.floor(diff / 60_000);
    if (mins < 1) return "just now";
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
  }

  // Hotkey recording
  function startRecording() {
    recording = true;
    hotkeyError = "";
  }

  function handleHotkeyKeydown(e: KeyboardEvent) {
    if (!recording) return;
    e.preventDefault();
    e.stopPropagation();

    // Ignore bare modifier keys.
    if (["Control", "Shift", "Alt", "Meta"].includes(e.key)) return;

    const parts: string[] = [];
    if (e.metaKey || e.ctrlKey) parts.push("CmdOrCtrl");
    if (e.shiftKey) parts.push("Shift");
    if (e.altKey) parts.push("Alt");

    // Need at least one modifier.
    if (parts.length === 0) {
      hotkeyError = "Shortcut must include Cmd/Ctrl";
      return;
    }

    parts.push(e.key.length === 1 ? e.key.toUpperCase() : e.key);
    const shortcut = parts.join("+");

    applyHotkey(shortcut);
  }

  async function applyHotkey(shortcut: string) {
    recording = false;
    hotkeyError = "";
    try {
      await invoke("set_hotkey", { shortcut });
      currentHotkey = shortcut;
    } catch (e) {
      hotkeyError = e instanceof Error ? e.message : String(e);
    }
  }

  function cancelRecording() {
    recording = false;
    hotkeyError = "";
  }

  async function handleActivateLicense() {
    if (!licenseKey.trim()) return;
    activating = true;
    activateError = "";
    activateSuccess = "";
    try {
      const result = await api.activate(licenseKey.trim());
      activateSuccess = result.message;
      licenseKey = "";
      await checkConnection();
    } catch (e) {
      activateError = e instanceof Error ? e.message : "Activation failed";
    } finally {
      activating = false;
    }
  }

  async function checkForUpdate() {
    checkingUpdate = true;
    updateError = "";
    try {
      const { check } = await import("@tauri-apps/plugin-updater");
      const update = await check();
      if (update) {
        updateAvailable = update.version;
      } else {
        updateAvailable = null;
      }
    } catch (e) {
      updateError = e instanceof Error ? e.message : "Update check failed";
    } finally {
      checkingUpdate = false;
    }
  }

  async function installUpdate() {
    updating = true;
    updateError = "";
    try {
      const { check } = await import("@tauri-apps/plugin-updater");
      const update = await check();
      if (update) {
        await update.downloadAndInstall();
        // Tauri will restart the app after install.
      }
    } catch (e) {
      updateError = e instanceof Error ? e.message : "Update failed";
      updating = false;
    }
  }

  function openPricing() {
    window.open("https://devrecall.dev/pricing", "_blank");
  }

  function formatHotkeyDisplay(hk: string): string {
    return hk
      .replace("CmdOrCtrl", navigator.platform.includes("Mac") ? "\u2318" : "Ctrl")
      .replace("Shift", "\u21E7")
      .replace("Alt", "\u2325")
      .replace(/\+/g, "");
  }

  onMount(() => {
    loadStatus();
  });
</script>

<svelte:window onkeydown={handleHotkeyKeydown} />

<div class="flex flex-col h-full overflow-y-auto">
  <div class="px-4 py-4 space-y-6">
    <!-- Sources -->
    <section>
      <h2 class="text-sm font-semibold mb-3">Sources</h2>
      {#if loading}
        <p class="text-sm text-zinc-500">Loading...</p>
      {:else if error}
        <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">{error}</div>
      {:else}
        <div class="space-y-2">
          {#each sources as src}
            <div class="flex items-center justify-between px-3 py-2.5 rounded-lg border
                        border-zinc-200 dark:border-zinc-700">
              <div>
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium capitalize">{src.name}</span>
                  {#if src.enabled}
                    <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>
                  {:else}
                    <span class="text-xs text-zinc-400">disabled</span>
                  {/if}
                </div>
                {#if src.enabled}
                  <div class="text-xs text-zinc-500 mt-0.5">
                    {formatSyncTime(src.synced_at)} &middot; {src.count} activities
                  </div>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <!-- Sync -->
    <section>
      <h2 class="text-sm font-semibold mb-3">Sync</h2>
      <button
        onclick={triggerSync}
        disabled={syncing}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
      >
        {syncing ? "Syncing..." : "Sync Now"}
      </button>
    </section>

    <!-- Shortcuts -->
    <section>
      <h2 class="text-sm font-semibold mb-3">Shortcuts</h2>
      <div class="flex items-center gap-3">
        <span class="text-sm text-zinc-600 dark:text-zinc-400">Quick chat:</span>
        {#if recording}
          <div class="flex items-center gap-2">
            <span class="text-sm px-3 py-1.5 rounded-md border-2 border-devrecall-500 bg-devrecall-50
                         dark:bg-devrecall-900/20 animate-pulse">
              Press new shortcut...
            </span>
            <button
              onclick={cancelRecording}
              class="text-xs text-zinc-500 hover:text-zinc-700 dark:hover:text-zinc-300"
            >
              Cancel
            </button>
          </div>
        {:else}
          <span class="text-sm font-mono px-2 py-1 rounded-md bg-zinc-100 dark:bg-zinc-800">
            {formatHotkeyDisplay(currentHotkey)}
          </span>
          <button
            onclick={startRecording}
            class="text-xs text-devrecall-600 dark:text-devrecall-500 hover:underline"
          >
            Change
          </button>
        {/if}
      </div>
      {#if hotkeyError}
        <div class="text-xs text-red-500 mt-1">{hotkeyError}</div>
      {/if}
    </section>

    <!-- License -->
    <section>
      <h2 class="text-sm font-semibold mb-3">License</h2>
      <div class="space-y-3">
        <div class="flex items-center gap-2">
          <span class="text-sm text-zinc-600 dark:text-zinc-400">Plan:</span>
          <span class="text-sm font-medium capitalize
                       {$licenseInfo.plan === 'free'
                         ? 'text-zinc-700 dark:text-zinc-300'
                         : 'text-devrecall-600 dark:text-devrecall-400'}">
            {$licenseInfo.plan}
          </span>
          {#if $licenseInfo.activated_at}
            <span class="text-xs text-zinc-400">
              (activated {new Date($licenseInfo.activated_at).toLocaleDateString()})
            </span>
          {/if}
        </div>

        {#if $licenseInfo.plan === "free"}
          <div class="space-y-2">
            <div class="flex gap-2">
              <input
                type="text"
                bind:value={licenseKey}
                placeholder="DR-PRO-XXXX-XXXX-XXXX"
                class="flex-1 px-3 py-2 text-sm rounded-lg border border-zinc-300
                       dark:border-zinc-600 bg-white dark:bg-zinc-800
                       focus:outline-none focus:ring-2 focus:ring-devrecall-500"
                onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") handleActivateLicense(); }}
              />
              <button
                onclick={handleActivateLicense}
                disabled={activating || !licenseKey.trim()}
                class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
                       hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
              >
                {activating ? "Activating..." : "Activate"}
              </button>
            </div>
            {#if activateError}
              <p class="text-xs text-red-500">{activateError}</p>
            {/if}
            {#if activateSuccess}
              <p class="text-xs text-green-600 dark:text-green-400">{activateSuccess}</p>
            {/if}
            <button
              onclick={openPricing}
              class="text-xs text-devrecall-600 dark:text-devrecall-500 hover:underline"
            >
              Get a license key at devrecall.dev/pricing
            </button>
          </div>
        {/if}
      </div>
    </section>

    <!-- About & Updates -->
    <section>
      <h2 class="text-sm font-semibold mb-3">About</h2>
      <div class="text-sm text-zinc-600 dark:text-zinc-400 space-y-2">
        <div>DevRecall Desktop v0.1.0</div>

        {#if updateAvailable}
          <div class="flex items-center gap-2 px-3 py-2 rounded-lg bg-devrecall-50
                      dark:bg-devrecall-900/20 border border-devrecall-200 dark:border-devrecall-800">
            <span class="text-sm text-devrecall-700 dark:text-devrecall-300">
              Update available: v{updateAvailable}
            </span>
            <button
              onclick={installUpdate}
              disabled={updating}
              class="px-3 py-1 text-xs font-medium rounded-md bg-devrecall-600 text-white
                     hover:bg-devrecall-700 disabled:opacity-50 transition-colors"
            >
              {updating ? "Installing..." : "Update Now"}
            </button>
          </div>
        {:else}
          <button
            onclick={checkForUpdate}
            disabled={checkingUpdate}
            class="text-xs text-devrecall-600 dark:text-devrecall-500 hover:underline disabled:opacity-50"
          >
            {checkingUpdate ? "Checking..." : "Check for updates"}
          </button>
        {/if}

        {#if updateError}
          <p class="text-xs text-red-500">{updateError}</p>
        {/if}

        <div class="text-xs text-zinc-400">
          All data stored locally on your device.
        </div>
      </div>
    </section>
  </div>
</div>
