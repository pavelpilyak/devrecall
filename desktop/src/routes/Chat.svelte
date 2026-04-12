<script lang="ts">
  import { api, type ChatStreamEvent } from "../lib/api";
  import { save, load } from "../lib/persist";

  // A tool call rendered inside an assistant message bubble.
  type ToolCall = {
    name: string;
    args: unknown;
    result?: unknown;
    error?: string;
    durationMs?: number;
    expanded: boolean;
  };

  // Each chat message is either a plain user line or an assistant turn
  // that may carry inline tool calls + an answer that streams in.
  // `status` is a transient phase label rendered while the agent is still
  // working ("Thinking…", "Calling list_activities…") so the user can see
  // what's happening between tool steps.
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

  async function sendMessage() {
    if (!message.trim() || loading) return;

    const userMessage = message.trim();
    message = "";
    error = "";
    loading = true;

    const now = new Date().toISOString();
    chatHistory = [
      ...chatHistory,
      { role: "user", content: userMessage, timestamp: now },
      { role: "assistant", content: "", tools: [], streaming: true, status: "Thinking…", timestamp: now },
    ];
    scrollToBottom();

    // The history we send to the server: only the prior turns, in the
    // {role, content} shape the agent expects. Tool pills are local UI.
    const historyForServer = chatHistory
      .slice(0, -1)
      .map((m) => ({ role: m.role, content: m.content }));

    try {
      await api.chatStream(userMessage, handleStreamEvent, {
        history: historyForServer.slice(0, -1), // drop the just-added user message
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
        next.status = ev.step > 1 ? `Thinking… (step ${ev.step})` : "Thinking…";
        break;
      case "token":
        // First streamed token clears the status pill.
        next.status = undefined;
        next.content = (next.content ?? "") + ev.token;
        break;
      case "tool_call":
        next.status = `Calling ${ev.tool_name}…`;
        next.tools.push({
          name: ev.tool_name,
          args: ev.tool_args,
          expanded: false,
        });
        break;
      case "tool_result": {
        next.status = "Thinking…";
        // Match the most recent unresolved pill for this tool.
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
        // The streamed tokens already populated next.content; only fall back
        // to ev.content if nothing was streamed (non-streaming providers).
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

  async function copyLastResponse() {
    const last = chatHistory.findLast((m) => m.role === "assistant");
    if (last) {
      await navigator.clipboard.writeText(last.content);
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Messages -->
  <div class="flex-1 overflow-y-auto px-4 py-3 space-y-3" bind:this={messagesEl}>
    {#if chatHistory.length === 0}
      <div class="flex items-center justify-center h-full">
        <div class="text-center space-y-3">
          <div class="text-3xl">&#128172;</div>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">Ask anything about your work history.</p>
          <div class="flex flex-wrap gap-2 justify-center">
            {#each [
              "What did I do yesterday?",
              "How many PRs did I review this month?",
              "Summarize my work this week",
            ] as suggestion}
              <button
                onclick={() => { message = suggestion; sendMessage(); }}
                class="text-xs px-3 py-1.5 rounded-full border border-zinc-300 dark:border-zinc-600
                       hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
              >
                {suggestion}
              </button>
            {/each}
          </div>
        </div>
      </div>
    {:else}
      {#each chatHistory as msg, msgIdx}
        <div class="text-sm {msg.role === 'user' ? 'text-right' : ''}">
          <div class="inline-block max-w-[80%] px-3 py-2 rounded-lg {msg.role === 'user'
            ? 'bg-devrecall-600 text-white'
            : 'bg-zinc-100 dark:bg-zinc-800'}">
            {#if msg.role === 'assistant' && msg.tools && msg.tools.length > 0}
              <div class="flex flex-wrap gap-1 mb-2">
                {#each msg.tools as tool, toolIdx}
                  <button
                    onclick={() => toggleTool(msgIdx, toolIdx)}
                    class="text-[11px] px-2 py-0.5 rounded-full border
                           {tool.error
                             ? 'border-red-400 text-red-600 dark:text-red-400'
                             : 'border-zinc-300 dark:border-zinc-600 text-zinc-600 dark:text-zinc-300'}
                           hover:bg-zinc-200 dark:hover:bg-zinc-700 transition-colors"
                    title={tool.error ?? "click to expand"}
                  >
                    {tool.name}
                    {#if tool.durationMs !== undefined}
                      <span class="opacity-60">{tool.durationMs}ms</span>
                    {/if}
                  </button>
                {/each}
              </div>
              {#each msg.tools as tool, toolIdx}
                {#if tool.expanded}
                  <pre class="text-[11px] bg-zinc-200 dark:bg-zinc-900 rounded px-2 py-1 mb-2 overflow-x-auto whitespace-pre-wrap">
<span class="opacity-60">args:</span> {compact(tool.args)}
{#if tool.error}<span class="text-red-500">error:</span> {tool.error}{:else}<span class="opacity-60">result:</span> {compact(tool.result)}{/if}
                  </pre>
                {/if}
              {/each}
            {/if}
            {#if msg.status}
              <p class="text-[11px] italic text-zinc-500 dark:text-zinc-400 mb-1 flex items-center gap-1">
                <span class="inline-block w-1.5 h-1.5 rounded-full bg-devrecall-500 animate-pulse"></span>
                {msg.status}
              </p>
            {/if}
            <p class="whitespace-pre-wrap">{msg.content}{#if msg.streaming && msg.content}<span class="animate-pulse">▍</span>{/if}</p>
          </div>
          {#if msg.timestamp}
            <div class="text-[10px] text-zinc-400 mt-0.5 {msg.role === 'user' ? 'text-right' : ''}">
              {new Date(msg.timestamp).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
            </div>
          {/if}
        </div>
      {/each}
    {/if}

    {#if error}
      <div class="text-sm text-red-500 px-3 py-2 bg-red-50 dark:bg-red-900/20 rounded-lg">
        {error}
      </div>
    {/if}
  </div>

  <!-- Input -->
  <div class="border-t border-zinc-200 dark:border-zinc-700 px-4 py-3">
    <div class="flex gap-2">
      <input
        type="text"
        bind:value={message}
        onkeydown={handleKeydown}
        placeholder="Ask about your work..."
        disabled={loading}
        class="flex-1 px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
               bg-white dark:bg-zinc-800 focus:outline-none focus:ring-2 focus:ring-devrecall-500
               disabled:opacity-50"
      />
      <button
        onclick={sendMessage}
        disabled={loading || !message.trim()}
        class="px-4 py-2 text-sm font-medium rounded-lg bg-devrecall-600 text-white
               hover:bg-devrecall-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        Send
      </button>
      {#if chatHistory.length > 0}
        <button
          onclick={copyLastResponse}
          class="px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          title="Copy last response"
        >
          Copy
        </button>
        <button
          onclick={clearHistory}
          class="px-3 py-2 text-sm rounded-lg border border-zinc-300 dark:border-zinc-600
                 hover:bg-zinc-50 dark:hover:bg-zinc-800 transition-colors"
          title="Clear chat"
        >
          Clear
        </button>
      {/if}
    </div>
  </div>
</div>
