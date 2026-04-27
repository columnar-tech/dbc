// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    // WebKitGTK 2.40+ native-Waylnd path is unreliable on 
    // NVIDIA/hybrid linux GPUs: DMABuf imports fail ("Failed to creatae EGL image from DMABuf")
    // and accelerated compositing often yields aaa blaank window.
    // The DMABuf disable is a belt-and-suspenders fallback for flaky NVIDIA
    // stacks. Upstream: https://bugs.webkit.org/show_bug.cgi?id=262607
    #[cfg(target_os = "linux")]
    std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");

    gui_lib::run()
}
