use serde::Serialize;

#[derive(Debug, thiserror::Error, Serialize)]
pub enum SidecarError {
    #[error("IO error: {0}")]
    Io(String),
    #[error("Timed out waiting for sidecar")]
    Timeout,
    #[error("Sidecar exited with code {code}: {stderr_tail}")]
    ExitStatus { code: i32, stderr_tail: String },
    #[error("Failed to parse sidecar output: {0}")]
    ParseError(String),
    #[error("Operation was cancelled")]
    Cancelled,
    #[error("Another dbc operation is in progress (pid: {owner_pid})")]
    Locked { owner_pid: u32 },
}

impl From<std::io::Error> for SidecarError {
    fn from(e: std::io::Error) -> Self {
        SidecarError::Io(e.to_string())
    }
}
