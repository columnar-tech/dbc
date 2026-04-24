<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { Sheet, SheetContent, SheetHeader, SheetTitle } from '$lib/components/ui/sheet';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { Skeleton } from '$lib/components/ui/skeleton';
  import { Progress } from '$lib/components/ui/progress';
  import { Dialog, DialogContent, DialogHeader, DialogTitle } from '$lib/components/ui/dialog';
  import type { DriverInfo } from '$lib/schema/info.js';

  interface Props {
    driverName: string;
    open: boolean;
  }

  let { driverName, open = $bindable() }: Props = $props();

  let info = $state<DriverInfo | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);

  let installing = $state(false);
  let installPhase = $state('');
  let installProgress = $state(0);
  let installDone = $state(false);
  let installFailed = $state(false);
  let installError = $state('');
  let progressOpen = $state(false);

  $effect(() => {
    if (open && driverName) {
      loading = true;
      error = null;
      info = null;
      invoke<DriverInfo>('get_driver_info', { name: driverName })
        .then(d => { info = d; })
        .catch(e => { error = String(e); })
        .finally(() => { loading = false; });
    }
  });

  async function install() {
    const jobId = crypto.randomUUID();

    installPhase = 'Starting…';
    installProgress = 0;
    installDone = false;
    installFailed = false;
    installError = '';
    installing = true;
    progressOpen = true;
    open = false;

    const unlisten = await listen(`install-progress:${jobId}`, (event: any) => {
      const envelope = event.payload;
      const kind = envelope?.kind;

      if (kind === 'install.progress') {
        const evt = envelope.payload;
        installPhase = evt.event ?? installPhase;
        if (evt.bytes && evt.total && evt.total > 0) {
          installProgress = Math.round((evt.bytes / evt.total) * 100);
        }
      } else if (kind === 'install.status') {
        const s = envelope.payload;
        installDone = true;
        installProgress = 100;
        const statusLabel: Record<string, string> = {
          'installed': 'Complete',
          'already installed': 'Already installed',
        };
        installPhase = statusLabel[s?.status] ?? s?.status ?? 'Complete';
        if (s?.message) installPhase += ` — ${s.message}`;
        if (s?.conflict) installPhase += ` (replaced ${s.conflict})`;
      } else if (kind === 'error') {
        installFailed = true;
        installError = envelope.payload?.message ?? 'Installation failed';
      }
    });

    try {
      const result = await invoke<{ status: string; message?: string; conflict?: string }>('install_driver', {
        driver: driverName,
        version: null,
        level: 'user',
        noVerify: false,
        jobId,
      });
      installDone = true;
      installProgress = 100;
      const statusLabel: Record<string, string> = {
        'installed': 'Complete',
        'already installed': 'Already installed',
      };
      installPhase = statusLabel[result.status] ?? result.status ?? 'Complete';
      if (result.message) installPhase += ` — ${result.message}`;
      if (result.conflict) installPhase += ` (replaced ${result.conflict})`;
    } catch (e) {
      installFailed = true;
      installError = String(e);
    } finally {
      installing = false;
      unlisten();
    }
  }

  function closeProgress() {
    progressOpen = false;
  }
</script>

<Sheet bind:open>
  <SheetContent side="right" class="w-full sm:w-[28rem] sm:max-w-[28rem] overflow-y-auto">
    <div class="p-6">
      <SheetHeader class="mb-4">
        <SheetTitle>{driverName}</SheetTitle>
      </SheetHeader>

      {#if loading}
        <div class="space-y-3">
          <Skeleton class="h-4 w-full" />
          <Skeleton class="h-4 w-3/4" />
          <Skeleton class="h-4 w-1/2" />
        </div>
      {:else if error}
        <p class="text-destructive">{error}</p>
      {:else if info}
        <div class="space-y-5">
          <p class="text-sm text-muted-foreground leading-relaxed">{info.description}</p>
          <div class="flex gap-2 flex-wrap">
            <Badge>{info.license}</Badge>
            <Badge variant="outline">v{info.version}</Badge>
          </div>
          <div>
            <p class="text-sm font-medium mb-2">Platforms</p>
            <div class="flex gap-1 flex-wrap">
              {#each info.packages as pkg}
                <Badge variant="secondary" class="text-xs">{pkg}</Badge>
              {/each}
            </div>
          </div>
          <Button onclick={install} disabled={installing} class="w-full">
            {installing ? 'Installing…' : 'Install'}
          </Button>
        </div>
      {/if}
    </div>
  </SheetContent>
</Sheet>

<Dialog
  open={progressOpen}
  onOpenChange={(v) => { if (!installing) progressOpen = v; }}
>
  <DialogContent>
    <DialogHeader>
      <DialogTitle>Installing {driverName}</DialogTitle>
    </DialogHeader>
    <div class="space-y-4 py-4">
      <p class="text-sm text-muted-foreground">{installPhase}</p>
      <Progress value={installProgress} class="w-full" />
      {#if installFailed}
        <p class="text-destructive text-sm">{installError}</p>
        <Button onclick={closeProgress}>Close</Button>
      {:else if installDone}
        <Button onclick={closeProgress}>Done</Button>
      {:else}
        <p class="text-xs text-muted-foreground">This may take a moment…</p>
      {/if}
    </div>
  </DialogContent>
</Dialog>
