use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Listener, Manager};
use tokio::sync::oneshot;

use crate::cmds::installed::InstallLevel;
use crate::error::SidecarError;
use crate::sidecar::Sidecar;
use crate::state::AppState;

#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct InstallStatus {
    pub status: String,
    pub driver: String,
    pub version: String,
    pub location: String,
    pub message: Option<String>,
    pub conflict: Option<String>,
    pub checksum: Option<String>,
}

#[tauri::command]
pub async fn install_driver(
    app: AppHandle,
    driver: String,
    version: Option<String>,
    level: InstallLevel,
    no_verify: bool,
    job_id: String,
) -> Result<InstallStatus, SidecarError> {
    if level == InstallLevel::System {
        return Err(SidecarError::Io(
            "System-level install is not supported in the GUI (MVP restriction)".to_string(),
        ));
    }

    let install_mutex = {
        let state = app.state::<AppState>();
        state.install_mutex.clone()
    };
    let _guard = install_mutex.try_lock().map_err(|_| {
        SidecarError::Io("Another install is already in progress".to_string())
    })?;

    let driver_arg = match &version {
        Some(v) => format!("{}@{}", driver, v),
        None => driver.clone(),
    };

    let mut args_owned = vec!["install".to_string(), driver_arg];
    if no_verify {
        args_owned.push("--no-verify".to_string());
    }
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();

    let app_clone = app.clone();
    let job_id_clone = job_id.clone();

    let last_status: Arc<Mutex<Option<InstallStatus>>> = Arc::new(Mutex::new(None));
    let last_status_clone = last_status.clone();

    let (cancel_tx, cancel_rx) = oneshot::channel::<()>();
    let cancel_tx_slot: Arc<Mutex<Option<oneshot::Sender<()>>>> =
        Arc::new(Mutex::new(Some(cancel_tx)));
    let cancel_tx_for_listener = cancel_tx_slot.clone();
    let cancel_event = format!("install-cancel:{}", job_id);
    let _event_id = app.listen(cancel_event, move |_| {
        if let Ok(mut guard) = cancel_tx_for_listener.lock() {
            if let Some(tx) = guard.take() {
                let _ = tx.send(());
            }
        }
    });

    let sidecar = Sidecar::new(app.clone());
    sidecar
        .run_stream::<serde_json::Value, _>(
            &args,
            move |event| {
                let event_name = format!("install-progress:{}", job_id_clone);
                let _ = app_clone.emit(&event_name, &event);

                if event.get("kind").and_then(|k| k.as_str()) == Some("install.status") {
                    if let Some(payload) = event.get("payload") {
                        if let Ok(status) =
                            serde_json::from_value::<InstallStatus>(payload.clone())
                        {
                            if let Ok(mut guard) = last_status_clone.lock() {
                                *guard = Some(status);
                            }
                        }
                    }
                }
            },
            cancel_rx,
            Duration::from_secs(300),
        )
        .await?;

    let final_status = last_status
        .lock()
        .ok()
        .and_then(|g| g.clone())
        .unwrap_or_else(|| InstallStatus {
            status: "installed".to_string(),
            driver,
            version: version.unwrap_or_default(),
            location: String::new(),
            message: None,
            conflict: None,
            checksum: None,
        });

    Ok(final_status)
}

#[tauri::command]
pub async fn install_driver_local(
    app: AppHandle,
    tarball_path: PathBuf,
    level: InstallLevel,
    no_verify: bool,
    job_id: String,
) -> Result<InstallStatus, SidecarError> {
    if level == InstallLevel::System {
        return Err(SidecarError::Io(
            "System-level install is not supported in the GUI (MVP restriction)".to_string(),
        ));
    }

    if !tarball_path.exists() {
        return Err(SidecarError::Io(format!(
            "File not found: {}",
            tarball_path.display()
        )));
    }

    let path_str = tarball_path.to_string_lossy().to_string();
    let mut args_owned = vec!["install".to_string(), path_str];
    if no_verify {
        args_owned.push("--no-verify".to_string());
    }
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();

    let app_clone = app.clone();
    let job_id_clone = job_id.clone();
    let (_cancel_tx, cancel_rx) = oneshot::channel::<()>();

    let sidecar = Sidecar::new(app);
    sidecar
        .run_stream::<serde_json::Value, _>(
            &args,
            move |event| {
                let event_name = format!("install-progress:{}", job_id_clone);
                let _ = app_clone.emit(&event_name, &event);
            },
            cancel_rx,
            Duration::from_secs(300),
        )
        .await?;

    let driver_name = tarball_path
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("unknown")
        .to_string();

    Ok(InstallStatus {
        status: "installed".to_string(),
        driver: driver_name,
        version: String::new(),
        location: String::new(),
        message: None,
        conflict: None,
        checksum: None,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_install_status_serializes() {
        let status = InstallStatus {
            status: "installed".to_string(),
            driver: "snowflake".to_string(),
            version: "1.0.0".to_string(),
            location: "/home/user/.local/share/ADBC/Drivers".to_string(),
            message: None,
            conflict: None,
            checksum: Some("sha256:abc123".to_string()),
        };
        let json = serde_json::to_string(&status).unwrap();
        assert!(json.contains("snowflake"));
        assert!(json.contains("sha256:abc123"));
    }

    #[test]
    fn test_install_status_deserializes() {
        let json = r#"{"status":"installed","driver":"snowflake","version":"1.0.0","location":"/tmp","message":null,"conflict":null,"checksum":null}"#;
        let status: InstallStatus = serde_json::from_str(json).unwrap();
        assert_eq!(status.driver, "snowflake");
        assert_eq!(status.version, "1.0.0");
    }

    #[test]
    fn test_install_status_optional_fields_absent() {
        let json = r#"{"status":"installed","driver":"duckdb","version":"","location":""}"#;
        let status: InstallStatus = serde_json::from_str(json).unwrap();
        assert!(status.message.is_none());
        assert!(status.checksum.is_none());
    }
}
