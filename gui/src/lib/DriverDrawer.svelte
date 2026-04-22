<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { Sheet, SheetContent, SheetHeader, SheetTitle } from '$lib/components/ui/sheet';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { Skeleton } from '$lib/components/ui/skeleton';
  import type { DriverInfo } from '$lib/schema/info.js';

  interface Props {
    driverName: string;
    open: boolean;
  }

  let { driverName, open = $bindable() }: Props = $props();

  let info = $state<DriverInfo | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    if (open && driverName) {
      loading = true;
      error = null;
      invoke<DriverInfo>('get_driver_info', { name: driverName })
        .then(d => { info = d; })
        .catch(e => { error = String(e); })
        .finally(() => { loading = false; });
    }
  });

  async function install() {
    await invoke('install_driver', {
      driver: driverName,
      version: null,
      level: 'user',
      noVerify: false,
      jobId: crypto.randomUUID(),
    });
    open = false;
  }
</script>

<Sheet bind:open>
  <SheetContent side="right" class="w-96">
    <SheetHeader>
      <SheetTitle>{driverName}</SheetTitle>
    </SheetHeader>

    {#if loading}
      <div class="space-y-3 mt-4">
        <Skeleton class="h-4 w-full" />
        <Skeleton class="h-4 w-3/4" />
        <Skeleton class="h-4 w-1/2" />
      </div>
    {:else if error}
      <p class="text-destructive mt-4">{error}</p>
    {:else if info}
      <div class="mt-4 space-y-4">
        <p class="text-sm text-muted-foreground">{info.description}</p>
        <div class="flex gap-2 flex-wrap">
          <Badge>{info.license}</Badge>
          <Badge variant="outline">v{info.version}</Badge>
        </div>
        <div>
          <p class="text-sm font-medium mb-1">Platforms</p>
          <div class="flex gap-1 flex-wrap">
            {#each info.packages as pkg}
              <Badge variant="secondary" class="text-xs">{pkg}</Badge>
            {/each}
          </div>
        </div>
        <Button onclick={install} class="w-full">Install</Button>
      </div>
    {/if}
  </SheetContent>
</Sheet>
