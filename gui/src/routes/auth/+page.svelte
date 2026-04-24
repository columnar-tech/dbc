<script lang="ts">
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
  import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs';

  let registryUrl = $state('https://dbc-cdn-private.columnar.tech');
  let apiKey = $state('');
  let logging = $state(false);
  let message = $state('');
  let messageOk = $state(false);

  // Device code flow state
  let deviceCode = $state('');
  let verificationUrl = $state('');
  let verificationUrlComplete = $state('');

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
    } catch (e) {
      messageOk = false;
      message = String(e);
    } finally {
      logging = false;
    }
  }

  async function logout() {
    try {
      const result = await invoke<{ status: string }>('auth_logout', { registryUrl, purge: false });
      messageOk = result.status === 'success';
      message = messageOk ? 'Logged out' : 'Logout failed';
    } catch (e) {
      messageOk = false;
      message = String(e);
    }
  }
</script>

<div class="p-6">
  <h1 class="text-2xl font-bold mb-6">Authentication</h1>

  <Card class="max-w-md">
    <CardHeader>
      <CardTitle>Registry Login</CardTitle>
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
          <Button onclick={loginDevice} disabled={logging} class="w-full">
            {logging ? 'Waiting for authorization…' : 'Login with Device Code'}
          </Button>
        </TabsContent>
        <TabsContent value="apikey" class="pt-4 space-y-2">
          <Input type="password" placeholder="API Key" bind:value={apiKey} />
          <Button onclick={loginApiKey} disabled={logging} class="w-full">
            {logging ? 'Authenticating…' : 'Login with API Key'}
          </Button>
        </TabsContent>
      </Tabs>

      {#if message}
        <p class="text-sm {messageOk ? 'text-green-600 dark:text-green-400' : 'text-destructive'}">{message}</p>
      {/if}

      <Button variant="outline" onclick={logout} class="w-full">Logout</Button>
    </CardContent>
  </Card>
</div>
