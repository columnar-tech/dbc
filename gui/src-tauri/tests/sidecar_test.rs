use gui_lib::error::SidecarError;

#[test]
fn test_sidecar_error_timeout() {
    let err = SidecarError::Timeout;
    assert_eq!(err.to_string(), "Timed out waiting for sidecar");
}

#[test]
fn test_sidecar_error_exit_status() {
    let err = SidecarError::ExitStatus {
        code: 1,
        stderr_tail: "error message".to_string(),
    };
    assert!(err.to_string().contains("code 1"));
}

#[test]
fn test_sidecar_error_locked() {
    let err = SidecarError::Locked { owner_pid: 1234 };
    assert!(err.to_string().contains("1234"));
}

#[test]
fn test_sidecar_error_parse() {
    let err = SidecarError::ParseError("invalid json".to_string());
    assert!(err.to_string().contains("invalid json"));
}

#[test]
fn test_sidecar_error_cancelled() {
    let err = SidecarError::Cancelled;
    assert_eq!(err.to_string(), "Operation was cancelled");
}

#[test]
fn test_sidecar_error_io() {
    let err = SidecarError::Io("connection refused".to_string());
    assert!(err.to_string().contains("connection refused"));
}

#[test]
fn test_sidecar_error_from_io() {
    let io_err = std::io::Error::new(std::io::ErrorKind::NotFound, "file not found");
    let err: SidecarError = io_err.into();
    assert!(matches!(err, SidecarError::Io(_)));
}

#[test]
fn test_sidecar_error_serialize() {
    let err = SidecarError::Timeout;
    let json = serde_json::to_string(&err).unwrap();
    assert!(!json.is_empty());
}

#[test]
fn test_sidecar_error_exit_status_stderr_in_message() {
    let err = SidecarError::ExitStatus {
        code: 2,
        stderr_tail: "not found".to_string(),
    };
    let msg = err.to_string();
    assert!(msg.contains("not found"));
    assert!(msg.contains("2"));
}

#[test]
fn test_sidecar_error_locked_message() {
    let err = SidecarError::Locked { owner_pid: 9999 };
    let msg = err.to_string();
    assert!(msg.contains("9999"));
    assert!(msg.contains("progress") || msg.contains("pid"));
}
