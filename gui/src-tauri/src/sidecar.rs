use std::time::Duration;

use serde::de::DeserializeOwned;
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::ShellExt;
use tokio::sync::oneshot;
use tokio::time::timeout;

use crate::error::SidecarError;
use crate::state::{AppState, LogEntry};

pub struct Sidecar {
    app: AppHandle,
}

fn now_rfc3339() -> String {
    chrono::Utc::now().to_rfc3339()
}

fn push_log(app: &AppHandle, command: &str, args: &[String], exit_code: Option<i32>, stderr_tail: String) {
    if let Ok(state) = app.try_state::<AppState>().ok_or(()) {
        state.push_log(LogEntry {
            timestamp: now_rfc3339(),
            command: command.to_string(),
            args: args.to_vec(),
            exit_code,
            stderr_tail,
        });
    }
}

impl Sidecar {
    pub fn new(app: AppHandle) -> Self {
        Self { app }
    }

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
            .args(full_args.clone());

        let result = timeout(timeout_duration, cmd.output())
            .await
            .map_err(|_| SidecarError::Timeout)?
            .map_err(|e| SidecarError::Io(e.to_string()));

        let output = match result {
            Ok(o) => o,
            Err(e) => {
                push_log(&self.app, args.first().copied().unwrap_or("dbc"), &full_args, None, e.to_string());
                return Err(e);
            }
        };

        let stderr_text = String::from_utf8_lossy(&output.stderr);
        let stderr_tail: String = stderr_text.lines().rev().take(10).collect::<Vec<_>>().into_iter().rev().collect::<Vec<_>>().join("\n");

        if !output.status.success() {
            let code = output.status.code().unwrap_or(-1);
            push_log(&self.app, args.first().copied().unwrap_or("dbc"), &full_args, Some(code), stderr_tail.clone());
            return Err(SidecarError::ExitStatus { code, stderr_tail });
        }

        push_log(&self.app, args.first().copied().unwrap_or("dbc"), &full_args, Some(0), stderr_tail);

        let stdout = String::from_utf8_lossy(&output.stdout);
        serde_json::from_str(stdout.trim())
            .map_err(|e| SidecarError::ParseError(e.to_string()))
    }

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
            .args(full_args.clone());

        let (mut rx, child) = cmd.spawn().map_err(|e| SidecarError::Io(e.to_string()))?;

        let deadline = tokio::time::sleep(timeout_duration);
        tokio::pin!(deadline);
        tokio::pin!(cancel);

        let mut stderr_lines: Vec<String> = Vec::new();
        let mut exit_code: Option<i32> = None;

        let result = loop {
            select! {
                _ = &mut deadline => {
                    let _ = child.kill();
                    break Err(SidecarError::Timeout);
                }
                _ = &mut cancel => {
                    let _ = child.kill();
                    break Err(SidecarError::Cancelled);
                }
                event = rx.recv() => {
                    match event {
                        Some(CommandEvent::Stdout(line)) => {
                            let line_str = String::from_utf8_lossy(&line);
                            if let Ok(parsed) = serde_json::from_str::<E>(&line_str) {
                                on_event(parsed);
                            }
                        }
                        Some(CommandEvent::Stderr(line)) => {
                            stderr_lines.push(String::from_utf8_lossy(&line).to_string());
                            if stderr_lines.len() > 10 {
                                stderr_lines.remove(0);
                            }
                        }
                        Some(CommandEvent::Terminated(status)) => {
                            exit_code = status.code;
                            break Ok(());
                        }
                        None => break Ok(()),
                        _ => {}
                    }
                }
            }
        };

        let stderr_tail = stderr_lines.join("\n");
        let log_exit = match &result {
            Ok(()) => exit_code.or(Some(0)),
            Err(SidecarError::Cancelled) => None,
            Err(SidecarError::Timeout) => None,
            Err(_) => Some(-1),
        };
        push_log(&self.app, args.first().copied().unwrap_or("dbc"), &full_args, log_exit, stderr_tail);

        result
    }
}
