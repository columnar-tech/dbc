// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    // WebKitGTK 2.40+ native-Wayland path is unreliable on NVIDIA/hybrid Linux
    // GPUs: DMABuf imports fail ("Failed to create EGL image from DMABuf") and
    // accelerated compositing often yields a blank window. Routing through
    // XWayland uses the battle-tested X11 path. Guards let pixi activation or
    // user-provided values win.
    // Upstream: https://bugs.webkit.org/show_bug.cgi?id=262607
    #[cfg(target_os = "linux")]
    {
        if std::env::var_os("GDK_BACKEND").is_none() {
            std::env::set_var("GDK_BACKEND", "x11");
        }
        if std::env::var_os("WEBKIT_DISABLE_DMABUF_RENDERER").is_none() {
            std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");
        }
    }

    gui_lib::run()
}
