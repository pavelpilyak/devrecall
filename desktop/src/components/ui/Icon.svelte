<script lang="ts">
  // Lucide wrapper — loads the UMD bundle from CDN once, then re-runs
  // createIcons whenever the referenced name changes.
  import { onMount, tick } from "svelte";

  let {
    name,
    size = 16,
    stroke = 1.5,
  } = $props<{ name: string; size?: number; stroke?: number }>();

  let host: HTMLElement | undefined = $state();

  async function ensureLucide(): Promise<any> {
    const w = window as any;
    if (w.lucide) return w.lucide;
    if (!w.__lucideLoading) {
      w.__lucideLoading = new Promise<void>((resolve, reject) => {
        const s = document.createElement("script");
        s.src = "https://unpkg.com/lucide@latest/dist/umd/lucide.js";
        s.async = true;
        s.onload = () => resolve();
        s.onerror = () => reject(new Error("Failed to load lucide"));
        document.head.appendChild(s);
      });
    }
    await w.__lucideLoading;
    return w.lucide;
  }

  async function render() {
    if (!host) return;
    try {
      const lucide = await ensureLucide();
      host.innerHTML = "";
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("width", String(size));
      svg.setAttribute("height", String(size));
      svg.setAttribute("stroke-width", String(stroke));
      svg.setAttribute("data-lucide", name);
      host.appendChild(svg);
      lucide.createIcons({
        attrs: { "stroke-width": stroke, width: size, height: size },
      });
    } catch {
      /* offline — icon omitted */
    }
  }

  onMount(() => {
    void render();
  });

  $effect(() => {
    // Re-render on prop change.
    void name; void size; void stroke;
    void tick().then(render);
  });
</script>

<span bind:this={host} class="icon" style="width:{size}px; height:{size}px"></span>

<style>
  .icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: currentColor;
    flex-shrink: 0;
  }
  .icon :global(svg) {
    stroke: currentColor;
  }
</style>
