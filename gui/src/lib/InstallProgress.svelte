<script lang="ts">
  import { listen, type UnlistenFn } from '@tauri-apps/api/event';
  import { Dialog, DialogContent, DialogHeader, DialogTitle } from '$lib/components/ui/dialog';
  import { Progress } from '$lib/components/ui/progress';
  import { Button } from '$lib/components/ui/button';
  import { invoke } from '@tauri-apps/api/core';

  interface Props {
    jobId: string;
    open: boolean;
  }

  let { jobId, open = $bindable() }: Props = $props();

  let phase = $state('Starting...');
  let progress = $state(0);
  let done = $state(false);
  let failed = $state(false);
  let errorMsg = $state('');
  let unlisten: UnlistenFn | null = null;

  $effect(() => {
    if (!open || !jobId) return;

    done = false;
    failed = false;
    phase = 'Starting...';
    progress = 0;

    listen(`install-progress:${jobId}`, (event: any) => {
      const payload = event.payload;
      const kind = payload?.kind;

      if (kind === 'install.progress') {
        const evt = payload.payload;
        phase = evt.event ?? phase;
        if (evt.bytes && evt.total && evt.total > 0) {
          progress = Math.round((evt.bytes / evt.total) * 100);
        }
      } else if (kind === 'install.status') {
        done = true;
        phase = 'Complete';
        progress = 100;
      } else if (kind === 'error') {
        failed = true;
        errorMsg = payload.payload?.message ?? 'Installation failed';
      }
    }).then(fn => { unlisten = fn; });

    return () => {
      unlisten?.();
      unlisten = null;
    };
  });

  async function cancel() {
    await invoke('emit', { event: `install-cancel:${jobId}` }).catch(() => {});
    open = false;
  }
</script>

<Dialog bind:open>
  <DialogContent>
    <DialogHeader>
      <DialogTitle>Installing Driver</DialogTitle>
    </DialogHeader>

    <div class="space-y-4 py-4">
      <p class="text-sm text-muted-foreground">{phase}</p>
      <Progress value={progress} class="w-full" />

      {#if failed}
        <p class="text-destructive text-sm">{errorMsg}</p>
        <Button onclick={() => open = false}>Close</Button>
      {:else if done}
        <Button onclick={() => open = false}>Done</Button>
      {:else}
        <Button variant="outline" onclick={cancel}>Cancel</Button>
      {/if}
    </div>
  </DialogContent>
</Dialog>
