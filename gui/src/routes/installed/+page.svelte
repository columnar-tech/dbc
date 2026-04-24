<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { Alert, AlertDescription } from '$lib/components/ui/alert';

  interface Driver {
    name: string;
    version: string;
    path: string;
  }

  let drivers = $state<Driver[]>([]);
  let loading = $state(true);
  let uninstallingName = $state<string | null>(null);
  let statusMessage = $state<{ text: string; ok: boolean } | null>(null);

  async function load() {
    loading = true;
    try {
      drivers = await invoke<Driver[]>('list_installed', { level: 'user' });
    } catch {
      drivers = [];
    } finally {
      loading = false;
    }
  }

  async function uninstall(name: string) {
    if (!confirm(`Uninstall ${name}?`)) return;
    uninstallingName = name;
    statusMessage = null;
    try {
      await invoke('uninstall_driver', { name, level: 'user' });
      statusMessage = { text: `${name} uninstalled successfully.`, ok: true };
      await load();
    } catch (e) {
      statusMessage = { text: `Failed to uninstall ${name}: ${String(e)}`, ok: false };
    } finally {
      uninstallingName = null;
    }
  }

  $effect(() => {
    load();
    const unlisten = listen('driver-installed', () => load());
    return () => { unlisten.then(fn => fn()); };
  });
</script>

<div class="p-6">
  <h1 class="text-2xl font-bold mb-6">Installed Drivers</h1>

  {#if statusMessage}
    <Alert class={"mb-4 " + (statusMessage.ok ? 'border-green-500' : 'border-destructive')}>
      <AlertDescription class={statusMessage.ok ? 'text-green-700 dark:text-green-400' : 'text-destructive'}>
        {statusMessage.text}
      </AlertDescription>
    </Alert>
  {/if}

  {#if loading}
    <p class="text-muted-foreground">Loading...</p>
  {:else if drivers.length === 0}
    <p class="text-muted-foreground">No drivers installed. Browse the Catalog to add some.</p>
  {:else}
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Version</TableHead>
          <TableHead>Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {#each drivers as driver}
          <TableRow>
            <TableCell class="font-medium">{driver.name}</TableCell>
            <TableCell><Badge variant="outline">{driver.version}</Badge></TableCell>
            <TableCell>
              <Button
                variant="destructive"
                size="sm"
                disabled={uninstallingName !== null}
                onclick={() => uninstall(driver.name)}
              >
                {uninstallingName === driver.name ? 'Uninstalling…' : 'Uninstall'}
              </Button>
            </TableCell>
          </TableRow>
        {/each}
      </TableBody>
    </Table>
  {/if}
</div>
