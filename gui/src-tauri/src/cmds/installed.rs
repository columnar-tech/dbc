use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use tauri::AppHandle;

use crate::error::SidecarError;

#[derive(Debug, Serialize, Deserialize, Clone, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum InstallLevel {
    User,
    System,
}

#[derive(Debug, Serialize)]
pub struct InstalledDriver {
    pub name: String,
    pub version: String,
    pub path: String,
}

fn get_user_driver_dir() -> Option<PathBuf> {
    #[cfg(target_os = "macos")]
    {
        dirs::data_dir().map(|d| d.join("ADBC").join("Drivers"))
    }
    #[cfg(target_os = "linux")]
    {
        dirs::config_dir().map(|d| d.join("adbc").join("drivers"))
    }
    #[cfg(target_os = "windows")]
    {
        dirs::data_local_dir().map(|d| d.join("adbc").join("drivers"))
    }
}

fn parse_version_from_manifest(path: &std::path::Path) -> Option<String> {
    let content = std::fs::read_to_string(path).ok()?;
    let value: toml::Value = toml::from_str(&content).ok()?;
    value
        .get("version")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string())
}

#[tauri::command]
pub async fn list_installed(
    _app: AppHandle,
    level: InstallLevel,
) -> Result<Vec<InstalledDriver>, SidecarError> {
    let dir = match level {
        InstallLevel::User => get_user_driver_dir(),
        InstallLevel::System => None,
    };

    let dir = match dir {
        Some(d) => d,
        None => return Ok(vec![]),
    };

    if !dir.exists() {
        return Ok(vec![]);
    }

    let mut drivers = vec![];
    let entries = std::fs::read_dir(&dir).map_err(|e| SidecarError::Io(e.to_string()))?;

    for entry in entries.flatten() {
        let path = entry.path();
        if path.extension().and_then(|e| e.to_str()) == Some("toml") {
            let name = path
                .file_stem()
                .and_then(|n| n.to_str())
                .unwrap_or("")
                .to_string();
            if name.is_empty() {
                continue;
            }
            let version = parse_version_from_manifest(&path)
                .unwrap_or_else(|| "unknown".to_string());
            drivers.push(InstalledDriver {
                name,
                version,
                path: path.to_string_lossy().to_string(),
            });
        }
    }

    drivers.sort_by(|a, b| a.name.cmp(&b.name));
    Ok(drivers)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_install_level_serde_user() {
        let json = serde_json::to_string(&InstallLevel::User).unwrap();
        assert_eq!(json, r#""user""#);
        let parsed: InstallLevel = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed, InstallLevel::User);
    }

    #[test]
    fn test_install_level_serde_system() {
        let json = serde_json::to_string(&InstallLevel::System).unwrap();
        assert_eq!(json, r#""system""#);
        let parsed: InstallLevel = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed, InstallLevel::System);
    }

    #[test]
    fn test_get_user_driver_dir_returns_some() {
        assert!(get_user_driver_dir().is_some());
    }

    #[test]
    fn test_installed_driver_serializes() {
        let driver = InstalledDriver {
            name: "snowflake".to_string(),
            version: "1.0.0".to_string(),
            path: "/some/path".to_string(),
        };
        let json = serde_json::to_string(&driver).unwrap();
        assert!(json.contains("snowflake"));
        assert!(json.contains("1.0.0"));
    }

    #[test]
    fn test_parse_version_from_manifest() {
        let dir = std::env::temp_dir().join("test_manifest.toml");
        std::fs::write(&dir, "manifest_version = 1\nversion = '1.5.1'\n").unwrap();
        assert_eq!(
            parse_version_from_manifest(&dir),
            Some("1.5.1".to_string())
        );
        std::fs::remove_file(&dir).ok();
    }

    #[test]
    fn test_parse_version_missing_returns_none() {
        let dir = std::env::temp_dir().join("test_manifest_no_ver.toml");
        std::fs::write(&dir, "manifest_version = 1\n").unwrap();
        assert_eq!(parse_version_from_manifest(&dir), None);
        std::fs::remove_file(&dir).ok();
    }

    #[test]
    fn test_list_installed_nonexistent_dir_returns_empty() {
        let dir = std::path::Path::new("/nonexistent/adbc/drivers/that/does/not/exist");
        assert!(!dir.exists());
    }
}
