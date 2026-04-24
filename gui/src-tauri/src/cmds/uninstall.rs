use serde::{Deserialize, Serialize};
use std::time::Duration;
use tauri::AppHandle;

use crate::cmds::installed::InstallLevel;
use crate::error::SidecarError;
use crate::sidecar::Sidecar;

#[derive(Debug, Deserialize, Serialize)]
pub struct UninstallStatus {
    pub status: String,
    pub driver: String,
}

#[derive(Debug, Deserialize)]
struct Envelope<T> {
    payload: T,
}

#[tauri::command]
pub async fn uninstall_driver(
    app: AppHandle,
    name: String,
    level: InstallLevel,
) -> Result<UninstallStatus, SidecarError> {
    if level == InstallLevel::System {
        return Err(SidecarError::Io(
            "System-level uninstall is not supported in the GUI (MVP restriction)".to_string(),
        ));
    }

    let sidecar = Sidecar::new(app);
    let envelope: Envelope<UninstallStatus> = sidecar
        .run_json(&["uninstall", &name, "-l", "user"], Duration::from_secs(60))
        .await?;
    Ok(envelope.payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_uninstall_status_serde_roundtrip() {
        let status = UninstallStatus {
            status: "success".to_string(),
            driver: "snowflake".to_string(),
        };
        let json = serde_json::to_string(&status).unwrap();
        let parsed: UninstallStatus = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.status, "success");
        assert_eq!(parsed.driver, "snowflake");
    }

    #[test]
    fn test_system_level_is_distinct_from_user() {
        assert_ne!(InstallLevel::System, InstallLevel::User);
    }

    #[test]
    fn test_envelope_deserialize_uninstall() {
        let json = r#"{"schema_version":1,"kind":"uninstall.complete","payload":{"status":"success","driver":"test"}}"#;
        let env: Envelope<UninstallStatus> = serde_json::from_str(json).unwrap();
        assert_eq!(env.payload.status, "success");
        assert_eq!(env.payload.driver, "test");
    }
}
