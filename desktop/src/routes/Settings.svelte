<script lang="ts">
  import { onMount } from "svelte";
  import { api, type SourceStatus } from "../lib/api";
  import { apiStatus, checkConnection, lastSyncAt } from "../lib/stores";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import SettingsSection from "../components/ui/SettingsSection.svelte";
  import SettingsRow from "../components/ui/SettingsRow.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import Chip from "../components/ui/Chip.svelte";
  import SyncDot from "../components/ui/SyncDot.svelte";
  import SourceDot from "../components/ui/SourceDot.svelte";

  let sources = $state<SourceStatus[]>([]);
  let loading = $state(true);
  let syncing = $state(false);
  let error = $state("");

  let lastSyncTime = $state<string | null>(null);

  let updateAvailable = $state<string | null>(null);
  let checkingUpdate = $state(false);
  let updating = $state(false);
  let updateError = $state("");

  type Provider = "ollama" | "openai" | "anthropic";
  let llmProvider = $state<Provider>("ollama");
  let llmModel = $state("");
  let llmBaseURL = $state("");
  let llmKey = $state("");
  let llmSaving = $state(false);
  let llmKeySaving = $state(false);
  let llmTesting = $state(false);
  let llmMsg = $state("");
  let llmError = $state("");
  let llmInitialized = $state(false);

  $effect(() => {
    const cur = $apiStatus?.llm;
    if (cur && !llmInitialized) {
      const p = (cur.provider || "ollama") as Provider;
      llmProvider = ["ollama", "openai", "anthropic"].includes(p) ? p : "ollama";
      llmModel = cur.model || "";
      llmInitialized = true;
    }
  });

  const PROVIDER_DEFAULTS: Record<Provider, { model: string; base_url: string }> = {
    ollama: { model: "gemma4", base_url: "http://localhost:11434" },
    openai: { model: "gpt-4o-mini", base_url: "" },
    anthropic: { model: "claude-haiku-4-5-20251001", base_url: "" },
  };

  async function saveLLMConfig() {
    llmSaving = true;
    llmMsg = "";
    llmError = "";
    try {
      await api.llmConfig({
        provider: llmProvider,
        model: llmModel.trim() || PROVIDER_DEFAULTS[llmProvider].model,
        base_url: llmBaseURL.trim(),
      });
      llmMsg = "Saved";
      await checkConnection();
    } catch (e) {
      llmError = e instanceof Error ? e.message : "Save failed";
    } finally {
      llmSaving = false;
    }
  }

  async function saveLLMKey() {
    if (llmProvider === "ollama") return;
    llmKeySaving = true;
    llmMsg = "";
    llmError = "";
    try {
      await api.llmKey(llmProvider, llmKey.trim());
      llmKey = "";
      llmMsg = "API key saved";
    } catch (e) {
      llmError = e instanceof Error ? e.message : "Failed to save key";
    } finally {
      llmKeySaving = false;
    }
  }

  function changeProvider(next: Provider) {
    if (next === llmProvider) return;
    llmProvider = next;
    llmModel = "";
    llmBaseURL = "";
    llmKey = "";
    llmMsg = "";
    llmError = "";
  }

  async function testLLM() {
    llmTesting = true;
    llmMsg = "";
    llmError = "";
    try {
      const r = await api.llmTest({
        provider: llmProvider,
        model: llmModel.trim() || PROVIDER_DEFAULTS[llmProvider].model,
        base_url: llmBaseURL.trim(),
      });
      llmMsg = `Connected to ${r.provider}`;
    } catch (e) {
      llmError = e instanceof Error ? e.message : "Test failed";
    } finally {
      llmTesting = false;
    }
  }

  async function loadStatus() {
    loading = true;
    error = "";
    try {
      const resp = await api.status();
      sources = resp.sources;
      const syncTimes = resp.sources
        .filter((s) => s.synced_at)
        .map((s) => new Date(s.synced_at!).getTime());
      if (syncTimes.length > 0) {
        lastSyncTime = new Date(Math.max(...syncTimes)).toISOString();
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
      lastSyncTime = new Date().toISOString();
      await loadStatus();
      lastSyncAt.set(Date.now());
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

  async function checkForUpdate() {
    checkingUpdate = true;
    updateError = "";
    try {
      const { check } = await import("@tauri-apps/plugin-updater");
      const update = await check();
      updateAvailable = update ? update.version : null;
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
      if (update) await update.downloadAndInstall();
    } catch (e) {
      updateError = e instanceof Error ? e.message : "Update failed";
      updating = false;
    }
  }

  onMount(() => {
    loadStatus();
  });
</script>

<div class="route-body">
  <PanelHeader title="Settings" meta="everything lives on this machine" />

  <div class="scroll">
    <div class="container">
      <SettingsSection title="Sync" desc="Pull fresh activity from every connected source.">
        {#snippet children()}
          <SettingsRow
            titleText="Sync now"
            meta={lastSyncTime ? `last sync · ${formatSyncTime(lastSyncTime)}` : "never synced"}
          >
            {#snippet right()}
              <Btn size="sm" variant="primary" disabled={syncing} onclick={triggerSync}>
                {#snippet children()}
                  <Icon name={syncing ? "loader" : "refresh-cw"} size={12} />
                  <span>{syncing ? "Syncing…" : "Sync"}</span>
                {/snippet}
              </Btn>
            {/snippet}
          </SettingsRow>
        {/snippet}
      </SettingsSection>

      <SettingsSection title="Sources" desc="OAuth tokens live on disk at ~/.devrecall/tokens/ (0600).">
        {#snippet children()}
          {#if loading}
            <div class="state">Loading sources…</div>
          {:else if error}
            <div class="error-inline">{error}</div>
          {:else}
            {#each sources as src (src.name)}
              <SettingsRow
                meta={src.enabled ? `${formatSyncTime(src.synced_at)} · ${src.count} activities` : "not connected"}
              >
                {#snippet title()}
                  <SourceDot source={src.name} />
                  <span style="text-transform: capitalize">{src.name}</span>
                {/snippet}
                {#snippet right()}
                  {#if src.enabled}
                    <SyncDot status="ok" />
                    <span class="ok-label">connected</span>
                  {:else}
                    <Btn size="sm">
                      {#snippet children()}<span>Connect</span>{/snippet}
                    </Btn>
                  {/if}
                {/snippet}
              </SettingsRow>
            {/each}
          {/if}
        {/snippet}
      </SettingsSection>

      <SettingsSection title="Model" desc="Pick the LLM that powers chat, standups, and summaries.">
        {#snippet children()}
          <SettingsRow titleText="Provider" meta="ollama runs locally · openai/anthropic require an API key">
            {#snippet right()}
              <select
                class="select"
                value={llmProvider}
                onchange={(e) => changeProvider(e.currentTarget.value as Provider)}
              >
                <option value="ollama">Ollama (local)</option>
                <option value="openai">OpenAI</option>
                <option value="anthropic">Anthropic</option>
              </select>
            {/snippet}
          </SettingsRow>

          <SettingsRow titleText="Model" meta={`default: ${PROVIDER_DEFAULTS[llmProvider].model}`}>
            {#snippet right()}
              <input
                class="text"
                type="text"
                bind:value={llmModel}
                placeholder={PROVIDER_DEFAULTS[llmProvider].model}
              />
            {/snippet}
          </SettingsRow>

          <SettingsRow titleText="Base URL" meta="optional · override the default endpoint">
            {#snippet right()}
              <input
                class="text"
                type="text"
                bind:value={llmBaseURL}
                placeholder={PROVIDER_DEFAULTS[llmProvider].base_url || "https://api.example.com"}
              />
            {/snippet}
          </SettingsRow>

          {#if llmProvider !== "ollama"}
            <SettingsRow titleText="API key" meta="stored on disk under ~/.devrecall/tokens/ (0600)">
              {#snippet right()}
                <input
                  class="text"
                  type="password"
                  bind:value={llmKey}
                  placeholder={llmProvider === "openai" ? "sk-..." : "sk-ant-..."}
                  autocomplete="off"
                />
                <Btn size="sm" disabled={llmKeySaving || !llmKey.trim()} onclick={saveLLMKey}>
                  {#snippet children()}<span>{llmKeySaving ? "Saving…" : "Save key"}</span>{/snippet}
                </Btn>
              {/snippet}
            </SettingsRow>
          {/if}

          <div class="llm-actions">
            <Btn size="sm" variant="primary" disabled={llmSaving} onclick={saveLLMConfig}>
              {#snippet children()}<span>{llmSaving ? "Saving…" : "Save"}</span>{/snippet}
            </Btn>
            <Btn size="sm" variant="ghost" disabled={llmTesting} onclick={testLLM}>
              {#snippet children()}<span>{llmTesting ? "Testing…" : "Test connection"}</span>{/snippet}
            </Btn>
            {#if llmMsg}<span class="ok-label">{llmMsg}</span>{/if}
          </div>
          {#if llmError}<div class="error-inline">{llmError}</div>{/if}
        {/snippet}
      </SettingsSection>

      <SettingsSection title="About">
        {#snippet children()}
          <SettingsRow titleText="DevRecall Desktop" meta="v0.1.0 · Tauri 2">
            {#snippet right()}
              {#if updateAvailable}
                <Chip variant="accent">
                  {#snippet children()}<span>v{updateAvailable} ready</span>{/snippet}
                </Chip>
                <Btn size="sm" variant="primary" disabled={updating} onclick={installUpdate}>
                  {#snippet children()}<span>{updating ? "Installing…" : "Update"}</span>{/snippet}
                </Btn>
              {:else}
                <Btn size="sm" variant="ghost" disabled={checkingUpdate} onclick={checkForUpdate}>
                  {#snippet children()}<span>{checkingUpdate ? "Checking…" : "Check for updates"}</span>{/snippet}
                </Btn>
              {/if}
            {/snippet}
          </SettingsRow>
          <SettingsRow titleText="Relay" meta="cf-worker · OAuth callbacks only · never user data">
            {#snippet right()}
              <Chip variant="accent">
                {#snippet children()}<span>● local-only</span>{/snippet}
              </Chip>
            {/snippet}
          </SettingsRow>
          {#if updateError}
            <div class="error-inline">{updateError}</div>
          {/if}
        {/snippet}
      </SettingsSection>
    </div>
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll { flex: 1; overflow: auto; background: var(--ink-1); padding: 28px 0; }
  .container {
    max-width: 720px;
    margin: 0 auto;
    padding: 0 40px;
  }

  .state {
    padding: 20px;
    text-align: center;
    font-size: 12px;
    color: var(--fg-3);
  }

  .error-inline {
    margin: 10px 16px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--danger);
  }
  .ok-label {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .select, .text {
    height: 28px;
    padding: 0 8px;
    border-radius: var(--r-2);
    border: 1px solid var(--border-strong);
    background: var(--ink-3);
    color: var(--fg-1);
    font-family: var(--font-mono);
    font-size: 12px;
    outline: none;
  }
  .select { padding-right: 24px; }
  .text { width: 220px; }
  .select:focus, .text:focus {
    border-color: var(--accent-line);
    box-shadow: 0 0 0 3px var(--accent-wash);
  }
  .llm-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--hairline);
  }

</style>
