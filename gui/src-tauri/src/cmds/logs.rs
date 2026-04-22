use tauri::{AppHandle, Manager};

use crate::error::SidecarError;
use crate::state::{AppState, LogEntry};

#[tauri::command]
pub fn get_logs(
    app: AppHandle,
    limit: Option<usize>,
    filter_command: Option<String>,
) -> Result<Vec<LogEntry>, SidecarError> {
    let state = app.state::<AppState>();
    let logs = state
        .logs
        .lock()
        .map_err(|e| SidecarError::Io(e.to_string()))?;

    let limit = limit.unwrap_or(500);
    let entries: Vec<LogEntry> = logs
        .iter()
        .filter(|e| {
            filter_command
                .as_ref()
                .map(|f| e.command.contains(f.as_str()))
                .unwrap_or(true)
        })
        .rev()
        .take(limit)
        .cloned()
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .collect();

    Ok(entries)
}

#[tauri::command]
pub fn clear_logs(app: AppHandle) -> Result<(), SidecarError> {
    let state = app.state::<AppState>();
    let mut logs = state
        .logs
        .lock()
        .map_err(|e| SidecarError::Io(e.to_string()))?;
    logs.clear();
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::state::AppState;

    #[test]
    fn test_log_entry_clone() {
        let entry = LogEntry {
            timestamp: "2026-01-01T00:00:00Z".to_string(),
            command: "install".to_string(),
            args: vec!["snowflake".to_string()],
            exit_code: Some(0),
            stderr_tail: String::new(),
        };
        let cloned = entry.clone();
        assert_eq!(cloned.command, "install");
        assert_eq!(cloned.exit_code, Some(0));
    }

    #[test]
    fn test_appstate_push_and_filter_manual() {
        let state = AppState::new();
        state.push_log(LogEntry {
            timestamp: "t1".to_string(),
            command: "install".to_string(),
            args: vec![],
            exit_code: Some(0),
            stderr_tail: String::new(),
        });
        state.push_log(LogEntry {
            timestamp: "t2".to_string(),
            command: "search".to_string(),
            args: vec![],
            exit_code: Some(0),
            stderr_tail: String::new(),
        });

        let logs = state.logs.lock().unwrap();
        let install_logs: Vec<_> = logs
            .iter()
            .filter(|e| e.command.contains("install"))
            .collect();
        assert_eq!(install_logs.len(), 1);
        assert_eq!(install_logs[0].command, "install");
    }
}
