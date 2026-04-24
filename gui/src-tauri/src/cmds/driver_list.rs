use std::path::PathBuf;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter};
use tokio::sync::oneshot;

use crate::cmds::installed::InstallLevel;
use crate::error::SidecarError;
use crate::sidecar::Sidecar;

#[derive(Debug, Deserialize, Serialize)]
pub struct InitResponse {
    pub driver_list_path: String,
    pub created: bool,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AddResponseDriver {
    pub name: String,
    pub version_constraint: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AddResponse {
    pub driver_list_path: String,
    pub driver: AddResponseDriver,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct RemoveResponseDriver {
    pub name: String,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct RemoveResponse {
    pub driver_list_path: String,
    pub driver: RemoveResponseDriver,
}

#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct SyncedDriver {
    pub name: String,
    pub version: String,
}

#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct SyncError {
    pub name: String,
    pub error: String,
}

#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct SyncStatus {
    pub installed: Vec<SyncedDriver>,
    pub skipped: Vec<SyncedDriver>,
    pub errors: Vec<SyncError>,
}

#[derive(Deserialize)]
struct Envelope<T> {
    payload: T,
}

#[derive(Debug, Serialize)]
pub struct DriverListEntry {
    pub name: String,
    pub version_constraint: Option<String>,
}

#[tauri::command]
pub async fn load_driver_list(path: PathBuf) -> Result<Vec<DriverListEntry>, SidecarError> {
    let content = match std::fs::read_to_string(&path) {
        Ok(c) => c,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(vec![]),
        Err(e) => return Err(SidecarError::Io(e.to_string())),
    };

    #[derive(Deserialize)]
    struct DriverSpec {
        version: Option<String>,
    }
    #[derive(Deserialize)]
    struct DriversList {
        #[serde(default)]
        drivers: std::collections::HashMap<String, DriverSpec>,
    }

    let parsed: DriversList = toml::from_str(&content)
        .map_err(|e| SidecarError::ParseError(e.to_string()))?;

    let mut entries: Vec<DriverListEntry> = parsed
        .drivers
        .into_iter()
        .map(|(name, spec)| DriverListEntry {
            name,
            version_constraint: spec.version,
        })
        .collect();
    entries.sort_by(|a, b| a.name.cmp(&b.name));
    Ok(entries)
}

#[tauri::command]
pub async fn init_driver_list(
    app: AppHandle,
    path: PathBuf,
) -> Result<InitResponse, SidecarError> {
    let path_str = path.to_string_lossy().to_string();
    let sidecar = Sidecar::new(app);
    let envelope: Envelope<InitResponse> = sidecar
        .run_json(&["init", &path_str], Duration::from_secs(10))
        .await?;
    Ok(envelope.payload)
}

#[tauri::command]
pub async fn add_driver(
    app: AppHandle,
    project_path: PathBuf,
    driver: String,
    version: Option<String>,
    prerelease: bool,
) -> Result<AddResponse, SidecarError> {
    let path_str = project_path.to_string_lossy().to_string();
    let driver_arg = match &version {
        Some(v) => format!("{}={}", driver, v),
        None => driver.clone(),
    };
    let mut args_owned = vec![
        "add".to_string(),
        driver_arg,
        "-p".to_string(),
        path_str,
    ];
    if prerelease {
        args_owned.push("--pre".to_string());
    }
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();
    let sidecar = Sidecar::new(app);
    let envelope: Envelope<AddResponse> = sidecar
        .run_json(&args, Duration::from_secs(30))
        .await?;
    Ok(envelope.payload)
}

#[tauri::command]
pub async fn remove_driver(
    app: AppHandle,
    project_path: PathBuf,
    driver: String,
) -> Result<RemoveResponse, SidecarError> {
    let path_str = project_path.to_string_lossy().to_string();
    let sidecar = Sidecar::new(app);
    let envelope: Envelope<RemoveResponse> = sidecar
        .run_json(
            &["remove", &driver, "-p", &path_str],
            Duration::from_secs(10),
        )
        .await?;
    Ok(envelope.payload)
}

#[tauri::command]
pub async fn sync_drivers(
    app: AppHandle,
    project_path: PathBuf,
    level: InstallLevel,
    no_verify: bool,
    job_id: String,
) -> Result<SyncStatus, SidecarError> {
    if level == InstallLevel::System {
        return Err(SidecarError::Io(
            "System-level sync is not supported in the GUI (MVP restriction)".to_string(),
        ));
    }

    let path_str = project_path.to_string_lossy().to_string();
    let mut args_owned = vec!["sync".to_string(), "-p".to_string(), path_str, "-l".to_string(), "user".to_string()];
    if no_verify {
        args_owned.push("--no-verify".to_string());
    }
    let args: Vec<&str> = args_owned.iter().map(|s| s.as_str()).collect();

    let app_clone = app.clone();
    let job_id_clone = job_id.clone();
    let (_cancel_tx, cancel_rx) = oneshot::channel::<()>();

    let last_status: std::sync::Arc<std::sync::Mutex<Option<SyncStatus>>> =
        std::sync::Arc::new(std::sync::Mutex::new(None));
    let last_status_clone = last_status.clone();

    let sidecar = Sidecar::new(app);
    sidecar
        .run_stream::<serde_json::Value, _>(
            &args,
            move |event| {
                let event_name = format!("sync-progress:{}", job_id_clone);
                let _ = app_clone.emit(&event_name, &event);
                if event.get("kind").and_then(|k| k.as_str()) == Some("sync.status") {
                    if let Some(payload) = event.get("payload") {
                        if let Ok(status) = serde_json::from_value::<SyncStatus>(payload.clone()) {
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
        .unwrap_or_else(|| SyncStatus {
            installed: vec![],
            skipped: vec![],
            errors: vec![],
        });

    Ok(final_status)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_init_response_serializes() {
        let r = InitResponse {
            driver_list_path: "/project/drivers.yaml".to_string(),
            created: true,
        };
        let json = serde_json::to_string(&r).unwrap();
        assert!(json.contains("drivers.yaml"));
        assert!(json.contains("true"));
    }

    #[test]
    fn test_add_response_deserializes() {
        let json = r#"{"driver_list_path":"/project/drivers.yaml","driver":{"name":"snowflake","version_constraint":"^1.0.0"}}"#;
        let r: AddResponse = serde_json::from_str(json).unwrap();
        assert_eq!(r.driver.name, "snowflake");
        assert_eq!(
            r.driver.version_constraint,
            Some("^1.0.0".to_string())
        );
    }

    #[test]
    fn test_remove_response_deserializes() {
        let json = r#"{"driver_list_path":"/project/drivers.yaml","driver":{"name":"snowflake"}}"#;
        let r: RemoveResponse = serde_json::from_str(json).unwrap();
        assert_eq!(r.driver.name, "snowflake");
    }

    #[test]
    fn test_sync_status_serializes() {
        let s = SyncStatus {
            installed: vec![SyncedDriver {
                name: "snowflake".to_string(),
                version: "1.0.0".to_string(),
            }],
            skipped: vec![],
            errors: vec![],
        };
        let json = serde_json::to_string(&s).unwrap();
        assert!(json.contains("snowflake"));
    }

    #[test]
    fn test_sync_error_serializes() {
        let e = SyncError {
            name: "broken-driver".to_string(),
            error: "network timeout".to_string(),
        };
        let json = serde_json::to_string(&e).unwrap();
        assert!(json.contains("broken-driver"));
        assert!(json.contains("network timeout"));
    }

    #[test]
    fn test_envelope_deserializes_payload_only() {
        let json = r#"{"schema_version":1,"kind":"driver_list.init","payload":{"driver_list_path":"/p","created":false}}"#;
        let env: Envelope<InitResponse> = serde_json::from_str(json).unwrap();
        assert_eq!(env.payload.driver_list_path, "/p");
        assert!(!env.payload.created);
    }
}
