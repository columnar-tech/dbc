<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';

  // Use a simple interface since InstalledDriver isn't in schema yet
  interface Driver {
    name: string;
    version: string;
    path: string;
  }

  let drivers = $state<Driver[]>([]);
  let loading = $state(true);

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
    try {
      await invoke('uninstall_driver', { name, level: 'user' });
      await load();
    } catch (e) {
      alert(String(e));
    }
  }

  $effect(() => { load(); });
</script>

<div class="p-6">
  <h1 class="text-2xl font-bold mb-6">Installed Drivers</h1>

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
              <Button variant="destructive" size="sm" onclick={() => uninstall(driver.name)}>
                Uninstall
              </Button>
            </TableCell>
          </TableRow>
        {/each}
      </TableBody>
    </Table>
  {/if}
</div>
