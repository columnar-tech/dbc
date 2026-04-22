use std::collections::VecDeque;
use std::sync::{Arc, Mutex};

use serde::Serialize;
use tokio::sync::Mutex as AsyncMutex;

#[derive(Debug, Clone, Serialize)]
pub struct LogEntry {
    pub timestamp: String,
    pub command: String,
    pub args: Vec<String>,
    pub exit_code: Option<i32>,
    pub stderr_tail: String,
}

pub struct AppState {
    pub install_mutex: Arc<AsyncMutex<()>>,
    pub logs: Mutex<VecDeque<LogEntry>>,
}

impl AppState {
    pub fn new() -> Self {
        Self {
            install_mutex: Arc::new(AsyncMutex::new(())),
            logs: Mutex::new(VecDeque::with_capacity(500)),
        }
    }

    pub fn push_log(&self, entry: LogEntry) {
        if let Ok(mut logs) = self.logs.lock() {
            if logs.len() >= 500 {
                logs.pop_front();
            }
            logs.push_back(entry);
        }
    }
}

impl Default for AppState {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_appstate_new_logs_empty() {
        let state = AppState::new();
        let logs = state.logs.lock().unwrap();
        assert!(logs.is_empty());
    }

    #[test]
    fn test_push_log_adds_entry() {
        let state = AppState::new();
        state.push_log(LogEntry {
            timestamp: "2026-01-01T00:00:00Z".to_string(),
            command: "install".to_string(),
            args: vec!["snowflake".to_string()],
            exit_code: Some(0),
            stderr_tail: String::new(),
        });
        let logs = state.logs.lock().unwrap();
        assert_eq!(logs.len(), 1);
        assert_eq!(logs[0].command, "install");
    }

    #[test]
    fn test_push_log_ring_buffer_evicts_front() {
        let state = AppState::new();
        for i in 0..501u32 {
            state.push_log(LogEntry {
                timestamp: format!("t{}", i),
                command: format!("cmd{}", i),
                args: vec![],
                exit_code: None,
                stderr_tail: String::new(),
            });
        }
        let logs = state.logs.lock().unwrap();
        assert_eq!(logs.len(), 500);
        assert_eq!(logs.front().map(|e| e.command.as_str()), Some("cmd1"));
    }

    #[test]
    fn test_log_entry_serializes() {
        let entry = LogEntry {
            timestamp: "2026-01-01T00:00:00Z".to_string(),
            command: "install".to_string(),
            args: vec!["snowflake".to_string()],
            exit_code: Some(0),
            stderr_tail: "some output".to_string(),
        };
        let json = serde_json::to_string(&entry).unwrap();
        assert!(json.contains("install"));
        assert!(json.contains("snowflake"));
    }
}
