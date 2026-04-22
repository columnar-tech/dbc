use serde::{Deserialize, Serialize};
use std::time::Duration;
use tauri::AppHandle;

use crate::error::SidecarError;
use crate::sidecar::Sidecar;

#[derive(Debug, Deserialize, Serialize)]
pub struct DriverInfo {
    pub driver: String,
    pub version: String,
    pub title: String,
    pub license: String,
    pub description: String,
    pub packages: Vec<String>,
}

#[derive(Debug, Deserialize)]
struct Envelope<T> {
    payload: T,
}

#[tauri::command]
pub async fn get_driver_info(
    app: AppHandle,
    name: String,
) -> Result<DriverInfo, SidecarError> {
    let sidecar = Sidecar::new(app);
    let envelope: Envelope<DriverInfo> = sidecar
        .run_json(&["info", &name], Duration::from_secs(30))
        .await?;
    Ok(envelope.payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_driver_info_serde_roundtrip() {
        let info = DriverInfo {
            driver: "snowflake".to_string(),
            version: "1.0.0".to_string(),
            title: "Snowflake ADBC Driver".to_string(),
            license: "Apache-2.0".to_string(),
            description: "Official Snowflake ADBC driver".to_string(),
            packages: vec!["adbc-driver-snowflake".to_string()],
        };
        let json = serde_json::to_string(&info).unwrap();
        let parsed: DriverInfo = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.driver, "snowflake");
        assert_eq!(parsed.version, "1.0.0");
        assert_eq!(parsed.packages.len(), 1);
    }

    #[test]
    fn test_envelope_deserialize_driver_info() {
        let json = r#"{"schema_version":1,"kind":"driver.info","payload":{"driver":"test","version":"0.1.0","title":"Test Driver","license":"MIT","description":"A test driver","packages":[]}}"#;
        let env: Envelope<DriverInfo> = serde_json::from_str(json).unwrap();
        assert_eq!(env.payload.driver, "test");
        assert_eq!(env.payload.version, "0.1.0");
    }
}
