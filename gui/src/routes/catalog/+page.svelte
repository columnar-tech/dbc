<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '$lib/components/ui/card';
  import { Skeleton } from '$lib/components/ui/skeleton';
  import DriverDrawer from '$lib/DriverDrawer.svelte';
  import type { SearchResponse } from '$lib/schema/search.js';

  let query = $state('');
  let includePrerelease = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let drivers = $state<SearchResponse['drivers']>([]);
  let selectedDriver = $state<string | null>(null);
  let selectedDriverInstalled = $state(false);
  let drawerOpen = $state(false);

  let debounceTimer: ReturnType<typeof setTimeout>;

  async function search() {
    loading = true;
    error = null;
    try {
      const result = await invoke<SearchResponse>('search_drivers', {
        query: query || null,
        includePrerelease,
        verbose: false,
      });
      drivers = result.drivers;
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    const q = query;
    const pre = includePrerelease;
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      loading = true;
      error = null;
      invoke<SearchResponse>('search_drivers', {
        query: q || null,
        includePrerelease: pre,
        verbose: false,
      }).then(result => {
        drivers = result.drivers;
      }).catch(e => {
        error = String(e);
      }).finally(() => {
        loading = false;
      });
    }, 250);
    return () => clearTimeout(debounceTimer);
  });

  $effect(() => {
    const unlisten = listen('driver-installed', () => search());
    const unlistenUninstall = listen('driver-uninstalled', () => search());
    return () => {
      unlisten.then(fn => fn());
      unlistenUninstall.then(fn => fn());
    };
  });

  function openDrawer(driverName: string, isInstalled: boolean) {
    selectedDriver = driverName;
    selectedDriverInstalled = isInstalled;
    drawerOpen = true;
  }
</script>

<div class="p-6">
  <div class="flex items-center gap-4 mb-6">
    <Input
      placeholder="Search drivers..."
      bind:value={query}
      class="max-w-sm"
    />
    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" bind:checked={includePrerelease} />
      Include pre-release
    </label>
  </div>

  {#if error}
    <div class="rounded-md bg-destructive/10 text-destructive p-4 mb-4">
      {error}
      <button onclick={search} class="ml-2 underline">Retry</button>
    </div>
  {/if}

  {#if loading}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each Array(6) as _}
        <Skeleton class="h-32 rounded-lg" />
      {/each}
    </div>
  {:else if drivers.length === 0}
    <p class="text-muted-foreground text-center py-12">No drivers match your search.</p>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      {#each drivers as driver}
        <button
          onclick={() => openDrawer(driver.driver, 'installed' in driver && !!driver.installed?.some(e => e.startsWith('user=>')))}
          class="text-left"
        >
          <Card class="hover:border-primary transition-colors cursor-pointer h-full">
            <CardHeader>
              <CardTitle class="text-base">{driver.driver}</CardTitle>
              {#if 'installed' in driver && driver.installed?.some(e => e.startsWith('user=>'))}
                <Badge variant="secondary" class="w-fit">Installed</Badge>
              {/if}
            </CardHeader>
            <CardContent>
              <CardDescription>{driver.description}</CardDescription>
            </CardContent>
          </Card>
        </button>
      {/each}
    </div>
  {/if}
</div>

{#if selectedDriver}
  <DriverDrawer
    driverName={selectedDriver}
    isInstalled={selectedDriverInstalled}
    bind:open={drawerOpen}
  />
{/if}
