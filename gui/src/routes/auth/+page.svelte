<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
  import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs';
  import { Badge } from '$lib/components/ui/badge';
  import { Separator } from '$lib/components/ui/separator';

  interface RegistryStatus {
    url: string;
    authenticated: boolean;
    auth_type?: string;
    license_valid: boolean;
  }

  let registryUrl = $state('https://dbc-cdn-private.columnar.tech');
  let apiKey = $state('');
  let logging = $state(false);
  let message = $state('');
  let messageOk = $state(false);

  let deviceCode = $state('');
  let verificationUrl = $state('');
  let verificationUrlComplete = $state('');

  let authStatus = $state<RegistryStatus[]>([]);
  let statusLoading = $state(true);
  let statusError = $state('');
  let loggingOutUrl = $state<string | null>(null);

  let authBusy = $derived(logging || loggingOutUrl !== null);

  async function loadStatus() {
    statusLoading = true;
    statusError = '';
    try {
      const result = await invoke<{ registries: RegistryStatus[] }>('auth_status');
      authStatus = result.registries;
    } catch (e) {
      authStatus = [];
      statusError = String(e);
    } finally {
      statusLoading = false;
    }
  }

  async function loginDevice() {
    logging = true;
    message = '';
    deviceCode = '';
    verificationUrl = '';
    verificationUrlComplete = '';
    const jobId = crypto.randomUUID();

    const unlisten = await listen(`auth-device-code:${jobId}`, (event: any) => {
      const payload = event.payload?.payload ?? event.payload;
      deviceCode = payload?.user_code ?? '';
      verificationUrl = payload?.verification_uri ?? '';
      verificationUrlComplete = payload?.verification_uri_complete ?? '';
    });

    try {
      const result = await invoke<{ status: string; message?: string }>('auth_login_device', {
        registryUrl,
        jobId,
      });
      messageOk = result.status === 'success';
      message = messageOk ? 'Login successful' : (result.message ?? 'Login failed');
      if (messageOk) await loadStatus();
    } catch (e) {
      messageOk = false;
      message = String(e);
    } finally {
      logging = false;
      deviceCode = '';
      verificationUrl = '';
      verificationUrlComplete = '';
      unlisten();
    }
  }

  async function loginApiKey() {
    logging = true;
    message = '';
    try {
      const result = await invoke<{ status: string; message?: string }>('auth_login_apikey', {
        registryUrl,
        apiKey,
      });
      apiKey = '';
      messageOk = result.status === 'success';
      message = messageOk ? 'Login successful' : (result.message ?? 'Login failed');
      if (messageOk) await loadStatus();
    } catch (e) {
      messageOk = false;
      message = String(e);
    } finally {
      logging = false;
    }
  }

  async function logout(url: string) {
    if (authBusy) return;
    loggingOutUrl = url;
    try {
      await invoke<{ status: string }>('auth_logout', { registryUrl: url, purge: false });
      messageOk = true;
      message = `Logged out of ${url}`;
      await loadStatus();
    } catch (e) {
      messageOk = false;
      message = String(e);
    } finally {
      loggingOutUrl = null;
    }
  }

  async function logoutPurge() {
    if (authBusy) return;
    if (!confirm('This will remove ALL stored credentials and the local license file for every registry. This cannot be undone. Continue?')) return;
    loggingOutUrl = '__purge__';
    try {
      await invoke<{ status: string }>('auth_logout', { registryUrl: registryUrl, purge: true });
      messageOk = true;
      message = 'All credentials purged';
      await loadStatus();
    } catch (e) {
      messageOk = false;
      message = String(e);
    } finally {
      loggingOutUrl = null;
    }
  }

  $effect(() => { loadStatus(); });
</script>

<div class="p-6 max-w-lg">
  <h1 class="text-2xl font-bold mb-6">Authentication</h1>

  {#if statusError}
    <div class="mb-4 rounded-md bg-destructive/10 text-destructive text-sm p-3">{statusError}</div>
  {/if}

  {#if !statusLoading && authStatus.length > 0}
    <Card class="mb-6">
      <CardHeader>
        <CardTitle class="text-base">Authenticated Registries</CardTitle>
      </CardHeader>
      <CardContent class="space-y-3">
        {#each authStatus as reg}
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <p class="text-sm font-medium truncate">{reg.url}</p>
              {#if reg.auth_type}
                <p class="text-xs text-muted-foreground">{reg.auth_type === 'oauth' ? 'OAuth' : 'API Key'}</p>
              {/if}
            </div>
            <div class="flex items-center gap-2 flex-shrink-0">
              <Badge variant={reg.authenticated ? 'default' : 'secondary'}>
                {reg.authenticated ? 'Authenticated' : 'Expired'}
              </Badge>
              <Button variant="ghost" size="sm" disabled={authBusy} onclick={() => logout(reg.url)}>
                {loggingOutUrl === reg.url ? 'Logging out…' : 'Logout'}
              </Button>
            </div>
          </div>
        {/each}
      </CardContent>
    </Card>
    <Separator class="mb-6" />
  {/if}

  <Card>
    <CardHeader>
      <CardTitle class="text-base">Add Registry Login</CardTitle>
    </CardHeader>
    <CardContent class="space-y-4">
      <Input placeholder="Registry URL" bind:value={registryUrl} />

      <Tabs value="device">
        <TabsList>
          <TabsTrigger value="device">Device Code</TabsTrigger>
          <TabsTrigger value="apikey">API Key</TabsTrigger>
        </TabsList>
        <TabsContent value="device" class="pt-4 space-y-3">
          {#if deviceCode}
            <div class="rounded-md border p-3 space-y-2">
              <p class="text-sm font-medium">Enter this code at the authorization page:</p>
              <p class="text-2xl font-mono font-bold tracking-widest">{deviceCode}</p>
              {#if verificationUrlComplete}
                <a href={verificationUrlComplete} target="_blank" rel="noopener noreferrer"
                   class="text-sm text-primary underline">Open authorization page</a>
              {:else if verificationUrl}
                <a href={verificationUrl} target="_blank" rel="noopener noreferrer"
                   class="text-sm text-primary underline">{verificationUrl}</a>
              {/if}
            </div>
          {/if}
          <Button onclick={loginDevice} disabled={authBusy} class="w-full">
            {logging ? 'Waiting for authorization…' : 'Login with Device Code'}
          </Button>
        </TabsContent>
        <TabsContent value="apikey" class="pt-4 space-y-2">
          <Input type="password" placeholder="API Key" bind:value={apiKey} />
          <Button onclick={loginApiKey} disabled={authBusy} class="w-full">
            {logging ? 'Authenticating…' : 'Login with API Key'}
          </Button>
        </TabsContent>
      </Tabs>

      {#if message}
        <p class="text-sm {messageOk ? 'text-green-600 dark:text-green-400' : 'text-destructive'}">{message}</p>
      {/if}
    </CardContent>
  </Card>

  <div class="mt-6 pt-4 border-t border-border">
    <p class="text-xs text-muted-foreground mb-2">Danger zone</p>
    <Button variant="destructive" size="sm" disabled={authBusy} onclick={logoutPurge}>
      Purge All Credentials
    </Button>
    <p class="text-xs text-muted-foreground mt-1">Removes all stored credentials and the local license file for every registry.</p>
  </div>
</div>
