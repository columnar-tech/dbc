use std::time::Duration;

use serde::de::DeserializeOwned;
use tauri::AppHandle;
use tauri_plugin_shell::ShellExt;
use tokio::sync::oneshot;
use tokio::time::timeout;

use crate::error::SidecarError;

pub struct Sidecar {
    app: AppHandle,
}

impl Sidecar {
    pub fn new(app: AppHandle) -> Self {
        Self { app }
    }

    /// Run dbc with args + ["--json"], wait for completion, parse JSON envelope.
    pub async fn run_json<T: DeserializeOwned>(
        &self,
        args: &[&str],
        timeout_duration: Duration,
    ) -> Result<T, SidecarError> {
        let mut full_args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
        full_args.push("--json".to_string());

        let shell = self.app.shell();
        let cmd = shell
            .sidecar("dbc")
            .map_err(|e| SidecarError::Io(e.to_string()))?
            .args(full_args);

        let output = timeout(timeout_duration, cmd.output())
            .await
            .map_err(|_| SidecarError::Timeout)?
            .map_err(|e| SidecarError::Io(e.to_string()))?;

        if !output.status.success() {
            let stderr_text = String::from_utf8_lossy(&output.stderr);
            let lines: Vec<&str> = stderr_text.lines().collect();
            let tail_lines: Vec<&str> = lines.iter().rev().take(10).cloned().collect();
            let stderr_tail = tail_lines.into_iter().rev().collect::<Vec<_>>().join("\n");
            return Err(SidecarError::ExitStatus {
                code: output.status.code().unwrap_or(-1),
                stderr_tail,
            });
        }

        let stdout = String::from_utf8_lossy(&output.stdout);
        serde_json::from_str(stdout.trim())
            .map_err(|e| SidecarError::ParseError(e.to_string()))
    }

    /// Run dbc with args + ["--json"], stream NDJSON lines via on_event callback.
    /// Returns when the stream ends or cancel is signalled.
    /// Child process is killed on timeout or cancellation.
    pub async fn run_stream<E, F>(
        &self,
        args: &[&str],
        on_event: F,
        cancel: oneshot::Receiver<()>,
        timeout_duration: Duration,
    ) -> Result<(), SidecarError>
    where
        E: DeserializeOwned,
        F: Fn(E) + Send + 'static,
    {
        use tauri_plugin_shell::process::CommandEvent;
        use tokio::select;

        let mut full_args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
        full_args.push("--json".to_string());

        let shell = self.app.shell();
        let cmd = shell
            .sidecar("dbc")
            .map_err(|e| SidecarError::Io(e.to_string()))?
            .args(full_args);

        let (mut rx, child) = cmd.spawn().map_err(|e| SidecarError::Io(e.to_string()))?;

        let deadline = tokio::time::sleep(timeout_duration);
        tokio::pin!(deadline);
        tokio::pin!(cancel);

        loop {
            select! {
                _ = &mut deadline => {
                    let _ = child.kill();
                    return Err(SidecarError::Timeout);
                }
                _ = &mut cancel => {
                    let _ = child.kill();
                    return Err(SidecarError::Cancelled);
                }
                event = rx.recv() => {
                    match event {
                        Some(CommandEvent::Stdout(line)) => {
                            let line_str = String::from_utf8_lossy(&line);
                            if let Ok(parsed) = serde_json::from_str::<E>(&line_str) {
                                on_event(parsed);
                            }
                        }
                        Some(CommandEvent::Terminated(_)) | None => {
                            break;
                        }
                        _ => {}
                    }
                }
            }
        }

        Ok(())
    }
}
