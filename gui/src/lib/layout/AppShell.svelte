<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    children: Snippet;
    sidebar?: Snippet;
  }

  let { children, sidebar }: Props = $props();
</script>

<div class="flex h-screen overflow-hidden bg-background text-foreground">
  {#if sidebar}
    <aside class="w-64 flex-shrink-0 overflow-y-auto border-r border-border">
      {@render sidebar()}
    </aside>
  {/if}
  <main class="flex-1 overflow-y-auto p-4">
    {@render children()}
  </main>
</div>
