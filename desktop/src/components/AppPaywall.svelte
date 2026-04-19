<script lang="ts">
  import { api } from "../lib/api";
  import { checkConnection } from "../lib/stores";
  import Btn from "./ui/Btn.svelte";
  import Icon from "./ui/Icon.svelte";

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

  function openPricing() {
    window.open("https://devrecall.dev/pricing", "_blank");
  }

  const features = [
    {
      icon: "link",
      title: "Integrations",
      meta: "Slack · Calendar · GitHub · GitLab · Bitbucket · Jira · Linear",
    },
    {
      icon: "zap",
      title: "AI summaries",
      meta: "Standups · weekly recaps · brag docs · perf reviews",
    },
    {
      icon: "message-square",
      title: "Chat over your local history",
      meta: "Agentic search · tool-aware answers · streaming replies",
    },
    {
      icon: "lock",
      title: "Local-first, encrypted backup",
      meta: "Data stays on device · optional E2E encrypted cloud backup",
    },
  ];
</script>

<div class="paywall">
  <div class="dot-grid"></div>

  <div class="inner">
    <div class="hdr">
      <div class="eyebrow">Desktop app · Pro / Team</div>
      <h1 class="title">Activate DevRecall to continue</h1>
      <p class="lede">
        The desktop is where DevRecall comes together — chat, integrations, and
        reports over your full local history. The CLI stays free for basic git
        standups.
      </p>
    </div>

    <div class="card">
      {#each features as f (f.title)}
        <div class="card-row">
          <div class="row-icon"><Icon name={f.icon} size={12} /></div>
          <div class="row-main">
            <div class="row-title">{f.title}</div>
            <div class="row-meta">{f.meta}</div>
          </div>
        </div>
      {/each}
    </div>

    <div class="price">
      <span class="price-num">$99</span>
      <span class="price-sep">·</span>
      <span>one-time</span>
      <span class="price-sep">·</span>
      <span>1 device</span>
    </div>

    <div class="actions">
      <Btn variant="primary" onclick={openPricing}>
        {#snippet children()}
          <span>Get a license</span>
          <Icon name="external-link" size={12} />
        {/snippet}
      </Btn>
      <Btn onclick={() => (showActivate = !showActivate)}>
        {#snippet children()}
          <span>{showActivate ? "Cancel" : "I have a key"}</span>
        {/snippet}
      </Btn>
    </div>

    {#if showActivate}
      <div class="activate-card">
        <div class="activate-head">
          <div class="activate-title">Activate this device</div>
          <div class="activate-meta">
            Paste your license key. Validated locally, stored at
            <code>~/.devrecall/license.json</code>.
          </div>
        </div>
        <div class="activate-row">
          <input
            type="text"
            spellcheck="false"
            autocomplete="off"
            bind:value={licenseKey}
            placeholder="XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
            onkeydown={(e: KeyboardEvent) => { if (e.key === "Enter") handleActivate(); }}
          />
          <Btn variant="primary" disabled={activating || !licenseKey.trim()} onclick={handleActivate}>
            {#snippet children()}
              <span>{activating ? "Activating…" : "Activate"}</span>
            {/snippet}
          </Btn>
        </div>
        {#if activateError}<div class="activate-err">{activateError}</div>{/if}
        {#if activateSuccess}<div class="activate-ok">{activateSuccess}</div>{/if}
      </div>
    {/if}
  </div>
</div>

<style>
  .paywall {
    position: relative;
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 48px 40px;
    background: var(--bg-canvas);
    overflow: auto;
  }
  .dot-grid {
    position: absolute;
    inset: 0;
    background-image: radial-gradient(var(--ink-4) 1px, transparent 1px);
    background-size: 24px 24px;
    opacity: 0.5;
    mask-image: radial-gradient(circle at center, black 10%, transparent 70%);
    -webkit-mask-image: radial-gradient(circle at center, black 10%, transparent 70%);
    pointer-events: none;
  }

  .inner {
    position: relative;
    width: 100%;
    max-width: 520px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .hdr { display: flex; flex-direction: column; gap: 8px; }
  .eyebrow {
    font-family: var(--font-mono);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
    color: var(--fg-3);
    font-weight: 500;
  }
  .title {
    margin: 0;
    font-size: 28px;
    font-weight: 600;
    letter-spacing: -0.018em;
    color: var(--fg-1);
    line-height: 1.2;
  }
  .lede {
    margin: 0;
    font-size: 14px;
    color: var(--fg-2);
    line-height: 1.55;
  }

  .card {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-3);
    overflow: hidden;
  }
  .card-row {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--hairline);
  }
  .card-row:last-child { border-bottom: none; }
  .row-icon {
    width: 20px;
    height: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--accent);
    flex-shrink: 0;
  }
  .row-main { min-width: 0; flex: 1; }
  .row-title {
    font-size: 13px;
    color: var(--fg-1);
    line-height: 1.3;
  }
  .row-meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    margin-top: 3px;
    line-height: 1.45;
  }

  .price {
    display: flex;
    align-items: baseline;
    gap: 8px;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--fg-3);
    letter-spacing: var(--tracking-mono);
  }
  .price-num {
    color: var(--fg-1);
    font-weight: 600;
    font-size: 14px;
  }
  .price-sep { color: var(--fg-4); }

  .actions {
    display: flex;
    gap: 8px;
    margin-top: -4px;
  }

  .activate-card {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-3);
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    animation: drFadeIn 180ms var(--ease-out);
  }
  @keyframes drFadeIn {
    from { opacity: 0; transform: translateY(-2px); }
    to { opacity: 1; transform: none; }
  }
  .activate-head { display: flex; flex-direction: column; gap: 2px; }
  .activate-title {
    font-size: 13px;
    font-weight: 500;
    color: var(--fg-1);
  }
  .activate-meta {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    line-height: 1.45;
  }
  .activate-meta code {
    font-family: inherit;
    padding: 1px 5px;
    border-radius: var(--r-1);
    background: var(--ink-3);
    color: var(--fg-2);
  }

  .activate-row {
    display: flex;
    gap: 8px;
  }
  .activate-row input {
    flex: 1;
    height: 30px;
    padding: 0 10px;
    border-radius: var(--r-2);
    border: 1px solid var(--border-strong);
    background: var(--ink-1);
    color: var(--fg-1);
    font-family: var(--font-mono);
    font-size: 12px;
    outline: none;
    min-width: 0;
  }
  .activate-row input:focus {
    border-color: var(--accent-line);
    background: var(--ink-3);
    box-shadow: 0 0 0 3px var(--accent-wash);
  }

  .activate-err {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--danger);
  }
  .activate-ok {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--ok);
  }
</style>
