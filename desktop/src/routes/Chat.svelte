<script lang="ts">
  import { onMount } from "svelte";
  import { api, type ChatStreamEvent } from "../lib/api";
  import { save, load } from "../lib/persist";
  import Markdown from "../components/Markdown.svelte";
  import PanelHeader from "../components/ui/PanelHeader.svelte";
  import Btn from "../components/ui/Btn.svelte";
  import Icon from "../components/ui/Icon.svelte";
  import Kbd from "../components/ui/Kbd.svelte";

  type ToolCall = {
    name: string;
    args: unknown;
    result?: unknown;
    error?: string;
    durationMs?: number;
    expanded: boolean;
  };

  type ChatMessage = {
    role: "user" | "assistant";
    content: string;
    tools?: ToolCall[];
    streaming?: boolean;
    status?: string;
    timestamp?: string;
  };

  let message = $state("");
  let chatHistory = $state<ChatMessage[]>(load<ChatMessage[]>("chat:history") ?? []);
  let loading = $state(false);
  let error = $state("");
  let messagesEl: HTMLDivElement;
  let textareaEl: HTMLTextAreaElement;

  const suggestions = [
    "What did I ship last week?",
    "Summarize my meetings yesterday",
    "Which PRs am I waiting on review for?",
  ];

  async function sendMessage(text?: string) {
    const body = (text ?? message).trim();
    if (!body || loading) return;

    message = "";
    error = "";
    loading = true;

    const now = new Date().toISOString();
    chatHistory = [
      ...chatHistory,
      { role: "user", content: body, timestamp: now },
      { role: "assistant", content: "", tools: [], streaming: true, status: "thinking…", timestamp: now },
    ];
    scrollToBottom();

    const historyForServer = chatHistory
      .slice(0, -1)
      .map((m) => ({ role: m.role, content: m.content }));

    try {
      await api.chatStream(body, handleStreamEvent, {
        history: historyForServer.slice(0, -1),
      });
    } catch (e) {
      error = e instanceof Error ? e.message : "Chat failed";
    } finally {
      const last = chatHistory[chatHistory.length - 1];
      if (last && last.role === "assistant") {
        chatHistory[chatHistory.length - 1] = { ...last, streaming: false, status: undefined };
      }
      loading = false;
      scrollToBottom();
      save("chat:history", chatHistory);
    }
  }

  function handleStreamEvent(ev: ChatStreamEvent) {
    const last = chatHistory[chatHistory.length - 1];
    if (!last || last.role !== "assistant") return;
    const next = { ...last, tools: [...(last.tools ?? [])] };

    switch (ev.type) {
      case "thinking":
        next.status = ev.step > 1 ? `thinking… (step ${ev.step})` : "thinking…";
        break;
      case "token":
        next.status = undefined;
        next.content = (next.content ?? "") + ev.token;
        break;
      case "tool_call":
        next.status = `calling ${ev.tool_name}…`;
        next.tools.push({
          name: ev.tool_name,
          args: ev.tool_args,
          expanded: false,
        });
        break;
      case "tool_result": {
        next.status = "thinking…";
        for (let i = next.tools.length - 1; i >= 0; i--) {
          const t = next.tools[i];
          if (t.name === ev.tool_name && t.result === undefined && !t.error) {
            next.tools[i] = {
              ...t,
              result: ev.tool_result,
              error: ev.tool_error,
              durationMs: ev.duration_ms,
            };
            break;
          }
        }
        break;
      }
      case "done":
        next.status = undefined;
        if (!next.content && ev.content) {
          next.content = ev.content;
        }
        break;
      case "error":
        error = ev.error;
        break;
    }

    chatHistory[chatHistory.length - 1] = next;
    scrollToBottom();
  }

  function toggleTool(msgIdx: number, toolIdx: number) {
    const msg = chatHistory[msgIdx];
    if (!msg || msg.role !== "assistant" || !msg.tools) return;
    const tools = [...msg.tools];
    tools[toolIdx] = { ...tools[toolIdx], expanded: !tools[toolIdx].expanded };
    chatHistory[msgIdx] = { ...msg, tools };
  }

  function compact(v: unknown): string {
    if (v === undefined || v === null) return "";
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }

  function scrollToBottom() {
    setTimeout(() => {
      if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
    }, 50);
  }

  onMount(() => {
    if (chatHistory.length > 0) scrollToBottom();
  });

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }

  function clearHistory() {
    chatHistory = [];
    error = "";
    save("chat:history", chatHistory);
  }
</script>

<div class="route-body">
  <PanelHeader title="Chat" meta="over your local work history">
    {#snippet actions()}
      <Btn size="sm" variant="ghost" onclick={clearHistory}>
        {#snippet children()}
          <Icon name="plus" size={12} />
          <span>New</span>
        {/snippet}
      </Btn>
    {/snippet}
  </PanelHeader>

  <div class="scroll" bind:this={messagesEl}>
    <div class="thread">
      {#if chatHistory.length === 0}
        <div class="empty">
          <div class="empty-title">Ask anything about your work history.</div>
          <div class="empty-sub">Queries and context stay on this machine.</div>
        </div>
      {:else}
        {#each chatHistory as msg, msgIdx}
          {#if msg.role === "user"}
            <div class="user-row">
              <div class="user-bubble">{msg.content}</div>
            </div>
          {:else}
            <div class="assistant-row">
              <div class="term-badge"><Icon name="terminal" size={12} /></div>
              <div class="assistant-body">
                {#if msg.tools && msg.tools.length > 0}
                  <div class="tool-pills">
                    {#each msg.tools as tool, toolIdx}
                      <button
                        class="pill"
                        class:err={tool.error}
                        onclick={() => toggleTool(msgIdx, toolIdx)}
                        title={tool.error ?? "click to expand"}
                      >
                        <span class="pill-name">{tool.name}</span>
                        {#if tool.durationMs !== undefined}
                          <span class="pill-dur">{tool.durationMs}ms</span>
                        {/if}
                      </button>
                    {/each}
                  </div>
                  {#each msg.tools as tool, toolIdx (toolIdx)}
                    {#if tool.expanded}
                      <pre class="tool-detail">
<span class="meta">args:</span> {compact(tool.args)}
{#if tool.error}<span class="err-text">error:</span> {tool.error}{:else}<span class="meta">result:</span> {compact(tool.result)}{/if}</pre>
                    {/if}
                  {/each}
                {/if}

                {#if msg.status}
                  <div class="status-line">
                    <span class="status-dot"></span>
                    <span>{msg.status}</span>
                  </div>
                {/if}

                {#if msg.content}
                  <div class="assistant-text">
                    <Markdown content={msg.content} />
                    {#if msg.streaming}<span class="caret">▍</span>{/if}
                  </div>
                {/if}
              </div>
            </div>
          {/if}
        {/each}
      {/if}

      {#if error}
        <div class="error-box">{error}</div>
      {/if}
    </div>
  </div>

  {#if chatHistory.length <= 1}
    <div class="suggest-row">
      {#each suggestions as s}
        <button class="suggest" onclick={() => sendMessage(s)}>{s}</button>
      {/each}
    </div>
  {/if}

  <div class="composer-wrap">
    <div class="composer">
      <span class="composer-prompt"><Icon name="chevron-right" size={14} /></span>
      <textarea
        bind:this={textareaEl}
        bind:value={message}
        onkeydown={handleKeydown}
        placeholder="Ask about your work history…"
        rows="1"
        disabled={loading}
      ></textarea>
      <Btn
        size="sm"
        variant="primary"
        disabled={loading || !message.trim()}
        onclick={() => sendMessage()}
      >
        {#snippet children()}
          <span>Send</span>
          <Kbd>{#snippet children()}↵{/snippet}</Kbd>
        {/snippet}
      </Btn>
    </div>
  </div>
</div>

<style>
  .route-body { flex: 1; display: flex; flex-direction: column; overflow: hidden; }
  .scroll {
    flex: 1;
    overflow: auto;
    background: var(--ink-1);
  }
  .thread {
    padding: 24px 40px;
    box-sizing: border-box;
  }
  .empty {
    padding: 80px 0 40px;
    text-align: center;
  }
  .empty-title {
    font-size: 15px;
    color: var(--fg-1);
    margin-bottom: 6px;
  }
  .empty-sub {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
  }

  .user-row {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 16px;
  }
  .user-bubble {
    max-width: 70%;
    background: var(--ink-3);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-3);
    padding: 8px 12px;
    font-size: 13px;
    color: var(--fg-1);
    white-space: pre-wrap;
    line-height: 1.5;
  }

  .assistant-row {
    display: flex;
    gap: 10px;
    margin-bottom: 20px;
  }
  .term-badge {
    width: 22px;
    height: 22px;
    border-radius: var(--r-2);
    background: var(--ink-3);
    border: 1px solid var(--border-strong);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    color: var(--accent);
  }
  .assistant-body {
    flex: 1;
    min-width: 0;
  }
  .assistant-text {
    font-size: 13px;
    color: var(--fg-1);
    line-height: 1.6;
  }
  .caret {
    display: inline-block;
    color: var(--accent);
    animation: drPulse 1s steps(2, end) infinite;
  }
  @keyframes drPulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
  }

  .tool-pills {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 8px;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-1);
    padding: 3px 8px;
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--fg-2);
    cursor: pointer;
    transition: background var(--dur-1) var(--ease-std);
  }
  .pill:hover { background: var(--ink-3); color: var(--fg-1); }
  .pill.err {
    border-color: rgba(255, 107, 107, 0.3);
    color: var(--danger);
  }
  .pill-dur { color: var(--fg-4); }
  .tool-detail {
    margin: 0 0 10px;
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-2);
    padding: 8px 10px;
    color: var(--fg-2);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-all;
  }
  .tool-detail .meta { color: var(--fg-4); }
  .tool-detail .err-text { color: var(--danger); }

  .status-line {
    display: flex;
    align-items: center;
    gap: 6px;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-3);
    margin-bottom: 10px;
  }
  .status-dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--accent);
    animation: drPulse 1.2s ease-in-out infinite;
  }

  .error-box {
    margin: 8px 0;
    font-size: 12px;
    color: var(--danger);
    background: var(--danger-wash);
    border: 1px solid rgba(255, 107, 107, 0.2);
    border-radius: var(--r-2);
    padding: 8px 10px;
  }

  .suggest-row {
    padding: 0 40px 10px;
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    width: 100%;
    box-sizing: border-box;
  }
  .suggest {
    background: var(--ink-2);
    border: 1px solid var(--border);
    border-radius: var(--r-2);
    padding: 5px 10px;
    color: var(--fg-2);
    font-size: 12px;
    cursor: pointer;
    transition: background var(--dur-1) var(--ease-std), color var(--dur-1) var(--ease-std);
  }
  .suggest:hover { background: var(--ink-3); color: var(--fg-1); }

  .composer-wrap {
    padding: 12px 40px 16px;
    border-top: 1px solid var(--border);
    background: var(--ink-1);
  }
  .composer {
    display: flex;
    align-items: flex-end;
    gap: 8px;
    background: var(--ink-2);
    border: 1px solid var(--border-strong);
    border-radius: var(--r-3);
    padding: 8px 10px;
    color: var(--accent);
  }
  .composer:focus-within {
    border-color: var(--accent-line);
    box-shadow: 0 0 0 3px var(--accent-wash);
  }
  .composer textarea {
    flex: 1;
    background: transparent;
    border: none;
    outline: none;
    color: var(--fg-1);
    font-family: var(--font-sans);
    font-size: 13px;
    resize: none;
    line-height: 1.5;
    padding: 4px 0;
    min-height: 20px;
    max-height: 160px;
  }
  .composer textarea::placeholder { color: var(--fg-4); }
  .composer-prompt {
    display: flex;
    align-self: center;
  }
</style>
