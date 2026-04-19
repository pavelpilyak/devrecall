<script lang="ts">
  import { marked } from "marked";
  import DOMPurify from "dompurify";

  interface Props {
    content: string;
    class?: string;
    variant?: "serif" | "sans";
  }

  let { content, class: className = "", variant = "sans" }: Props = $props();

  marked.setOptions({ breaks: true, gfm: true });

  let html = $derived(DOMPurify.sanitize(marked.parse(content || "") as string));
</script>

<div class="md" class:serif={variant === "serif"} class:sans={variant === "sans"}>
  <div class={className}>
    {@html html}
  </div>
</div>

<style>
  .md :global(h1) {
    font-size: 26px;
    font-weight: 600;
    letter-spacing: -0.014em;
    margin: 0 0 12px;
    color: var(--fg-1);
  }
  .md :global(h2) {
    font-size: 20px;
    font-weight: 600;
    letter-spacing: -0.012em;
    margin: 24px 0 10px;
    color: var(--fg-1);
  }
  .md :global(h3) {
    font-size: 15px;
    font-weight: 600;
    margin: 18px 0 8px;
    color: var(--fg-1);
  }
  .md :global(p) {
    margin: 0 0 10px;
    line-height: 1.7;
    color: var(--fg-1);
  }
  .md :global(ul),
  .md :global(ol) {
    margin: 0 0 12px;
    padding-left: 20px;
    color: var(--fg-1);
  }
  .md :global(li) {
    margin-bottom: 6px;
    line-height: 1.7;
  }
  .md :global(code) {
    font-family: var(--font-mono);
    font-size: 0.9em;
    background: var(--ink-3);
    padding: 2px 6px;
    border-radius: var(--r-1);
    color: var(--fg-1);
  }
  .md :global(pre) {
    background: var(--ink-2);
    border: 1px solid var(--border);
    padding: 12px 14px;
    border-radius: var(--r-2);
    overflow-x: auto;
    margin: 0 0 12px;
  }
  .md :global(pre code) { background: none; padding: 0; }
  .md :global(blockquote) {
    border-left: 3px solid var(--accent-line);
    padding-left: 12px;
    margin: 0 0 12px;
    color: var(--fg-2);
    font-style: italic;
  }
  .md :global(a) {
    color: var(--accent);
    text-decoration: underline;
    text-underline-offset: 2px;
    text-decoration-color: var(--accent-line);
  }
  .md :global(a:hover) { color: var(--mint-200); }
  .md :global(strong) { font-weight: 600; color: var(--fg-1); }
  .md :global(hr) {
    border: none;
    border-top: 1px solid var(--border);
    margin: 20px 0;
  }
  .md :global(table) {
    width: 100%;
    border-collapse: collapse;
    margin-bottom: 12px;
    font-size: 13px;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--border);
    padding: 8px 10px;
    text-align: left;
  }
  .md :global(th) {
    font-weight: 600;
    background: var(--ink-2);
    color: var(--fg-2);
    font-family: var(--font-mono);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: var(--tracking-caps);
  }

  .md.serif :global(p),
  .md.serif :global(li),
  .md.serif :global(ul),
  .md.serif :global(ol) {
    font-family: var(--font-serif);
    font-size: 15px;
    line-height: 1.7;
  }
  .md.serif :global(h2),
  .md.serif :global(h3) {
    font-family: var(--font-sans);
  }

  .md :global(*:last-child) { margin-bottom: 0; }
</style>
