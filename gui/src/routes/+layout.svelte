<script lang="ts">
  import '../app.css';
  import AppShell from '$lib/layout/AppShell.svelte';
  import { page } from '$app/stores';

  let { children } = $props();

  let isDark = $state(
    typeof localStorage !== 'undefined'
      ? localStorage.getItem('theme') === 'dark'
      : false
  );

  $effect(() => {
    if (isDark) {
      document.documentElement.classList.add('dark');
      localStorage.setItem('theme', 'dark');
    } else {
      document.documentElement.classList.remove('dark');
      localStorage.setItem('theme', 'light');
    }
  });

  const navItems = [
    { href: '/catalog', label: 'Catalog', icon: '🔍' },
    { href: '/installed', label: 'Installed', icon: '📦' },
    { href: '/project', label: 'Project', icon: '📋' },
    { href: '/auth', label: 'Auth', icon: '🔑' },
    { href: '/logs', label: 'Logs', icon: '📝' },
  ];
</script>

<AppShell>
  {#snippet sidebar()}
    <nav class="p-4 flex flex-col gap-1">
      <div class="text-lg font-bold mb-4 px-2">dbc</div>
      {#each navItems as item}
        <a
          href={item.href}
          class="flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors
            {$page.url.pathname === item.href
              ? 'bg-accent text-accent-foreground'
              : 'hover:bg-accent/50'}"
        >
          <span>{item.icon}</span>
          <span>{item.label}</span>
        </a>
      {/each}
      <div class="mt-auto pt-4">
        <button
          onclick={() => isDark = !isDark}
          class="w-full px-3 py-2 rounded-md text-sm hover:bg-accent/50 text-left"
        >
          {isDark ? '☀️ Light' : '🌙 Dark'}
        </button>
      </div>
    </nav>
  {/snippet}
  {@render children()}
</AppShell>
