//! Whether to let WebKitGTK use its DMA-BUF renderer.
//!
//! Since 2.42 that renderer is the default: the web process allocates its
//! buffers through GBM on a DRM render node and hands the file descriptors to
//! the UI process, which imports them into EGL. It is developed against Mesa.
//! On the NVIDIA driver the allocation fails for some format and modifier
//! combinations, and the failure mode is a window that renders nothing at all,
//! with `Failed to create GBM buffer` on stderr and no error anywhere the
//! application can see. A machine with no render node at all — a container, a
//! VM with no GPU passthrough — has nothing for the renderer to allocate from
//! either.
//!
//! So this guesses, and it guesses in the direction the two mistakes make
//! obvious: disabling the renderer where it would have worked costs
//! shared-memory buffers and some CPU, which for an alert console is nothing
//! anybody notices. Leaving it on where it does not work costs a window that
//! shows nothing, which is the whole application.
//!
//! `WEBKIT_DISABLE_DMABUF_RENDERER` in the environment always wins, and
//! `webkit_dmabuf` in the config file decides what happens when it is unset.
//! Nothing here ever removes a variable the operator exported.

use std::path::{Path, PathBuf};

use serde::Deserialize;

/// The variable WebKitGTK reads. Set to `1` to take the renderer out.
pub const DMABUF_ENV: &str = "WEBKIT_DISABLE_DMABUF_RENDERER";

/// What the config file asks for.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum DmabufPolicy {
    /// Probe the machine and disable the renderer where it looks unsupported.
    #[default]
    Auto,
    /// Never disable it: the machine says the guess is wrong about it.
    On,
    /// Always disable it, without probing.
    Off,
}

/// What the machine looks like, as far as this decision is concerned.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct Probe {
    /// DRM render nodes the process can see. Empty is disqualifying on its own.
    pub render_nodes: Vec<String>,
    /// Whether a render node is driven by the NVIDIA driver.
    pub nvidia: bool,
    /// `nvidia_drm`'s modeset parameter, when it is readable. `Some(false)`
    /// means GBM is certainly unavailable; `None` only means the parameter
    /// could not be read, which is normal for an unprivileged process.
    pub nvidia_modeset: Option<bool>,
}

/// Reads the machine, relative to a root so tests can supply a fixture.
pub fn probe(root: &Path) -> Probe {
    let mut render_nodes = Vec::new();
    if let Ok(entries) = std::fs::read_dir(root.join("dev/dri")) {
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().to_string();
            if name.starts_with("renderD") {
                render_nodes.push(name);
            }
        }
    }
    render_nodes.sort();

    // The driver behind the node is the reliable signal; `/proc/driver/nvidia`
    // is the fallback for a process that cannot resolve the sysfs link.
    let nvidia = render_nodes.iter().any(|node| {
        std::fs::read_link(root.join("sys/class/drm").join(node).join("device/driver"))
            .ok()
            .and_then(|target| {
                target
                    .file_name()
                    .map(|name| name.to_string_lossy().to_string())
            })
            .is_some_and(|driver| driver == "nvidia")
    }) || root.join("proc/driver/nvidia/version").exists();

    let nvidia_modeset =
        std::fs::read_to_string(root.join("sys/module/nvidia_drm/parameters/modeset"))
            .ok()
            .and_then(|value| match value.trim() {
                "Y" | "1" => Some(true),
                "N" | "0" => Some(false),
                _ => None,
            });

    Probe {
        render_nodes,
        nvidia,
        nvidia_modeset,
    }
}

/// What to do about the variable.
#[derive(Debug, PartialEq, Eq)]
pub enum Decision {
    /// Leave the environment alone, for this reason.
    Leave(String),
    /// Export `WEBKIT_DISABLE_DMABUF_RENDERER=1`, for this reason.
    Disable(String),
}

/// The whole decision, as a pure function of the policy, the environment and
/// the probe — which is what makes it testable without a GPU.
pub fn decide(policy: DmabufPolicy, env_set: bool, probe: &Probe) -> Decision {
    if env_set {
        // Deliberate and specific beats a guess, in both directions: somebody
        // debugging a rendering problem sets this by hand and must not have it
        // second-guessed.
        return Decision::Leave(format!("{DMABUF_ENV} is already set"));
    }
    match policy {
        DmabufPolicy::Off => Decision::Disable("webkit_dmabuf = \"off\"".to_string()),
        DmabufPolicy::On => Decision::Leave("webkit_dmabuf = \"on\"".to_string()),
        DmabufPolicy::Auto => auto(probe),
    }
}

fn auto(probe: &Probe) -> Decision {
    if probe.render_nodes.is_empty() {
        return Decision::Disable(
            "no DRM render node, so there is nothing for the renderer to allocate from".to_string(),
        );
    }
    if probe.nvidia {
        let detail = match probe.nvidia_modeset {
            Some(false) => {
                "the NVIDIA driver with nvidia_drm modeset off, where GBM is unavailable"
            }
            _ => "the NVIDIA driver, whose GBM refuses allocations WebKitGTK asks for",
        };
        return Decision::Disable(detail.to_string());
    }
    Decision::Leave("the render node looks like one WebKitGTK can allocate from".to_string())
}

/// Applies the decision to this process, and says what it did and why.
///
/// Linux only. The renderer is a WebKitGTK thing, and probing `/dev/dri` on a
/// platform that has no such path would read every machine as unsupported.
pub fn apply(policy: DmabufPolicy) {
    if !cfg!(target_os = "linux") {
        return;
    }
    let env_set = std::env::var_os(DMABUF_ENV).is_some();
    match decide(policy, env_set, &probe(&PathBuf::from("/"))) {
        Decision::Leave(reason) => {
            eprintln!("promview-desktop: WebKitGTK DMA-BUF renderer left as it is: {reason}");
        }
        Decision::Disable(reason) => {
            // Before the Tauri builder exists, which is the only moment early
            // enough: the web process reads this when it is spawned, and it
            // inherits the environment this process has by then.
            std::env::set_var(DMABUF_ENV, "1");
            eprintln!(
                "promview-desktop: disabled the WebKitGTK DMA-BUF renderer because of {reason}"
            );
            eprintln!(
                "promview-desktop: set webkit_dmabuf = \"on\" in the config file if this machine renders without it"
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn nvidia_probe() -> Probe {
        Probe {
            render_nodes: vec!["renderD128".to_string()],
            nvidia: true,
            nvidia_modeset: Some(true),
        }
    }

    fn mesa_probe() -> Probe {
        Probe {
            render_nodes: vec!["renderD128".to_string()],
            nvidia: false,
            nvidia_modeset: None,
        }
    }

    #[test]
    fn the_environment_is_never_second_guessed() {
        // Somebody debugging a rendering problem sets this by hand; a guess
        // that overrode it would make the obvious experiment impossible.
        for policy in [DmabufPolicy::Auto, DmabufPolicy::On, DmabufPolicy::Off] {
            let decision = decide(policy, true, &nvidia_probe());
            assert!(matches!(decision, Decision::Leave(_)), "{decision:?}");
        }
    }

    #[test]
    fn auto_disables_the_renderer_on_nvidia() {
        let decision = decide(DmabufPolicy::Auto, false, &nvidia_probe());
        assert!(matches!(decision, Decision::Disable(_)), "{decision:?}");
    }

    #[test]
    fn auto_says_so_when_modeset_makes_it_certain() {
        let probe = Probe {
            nvidia_modeset: Some(false),
            ..nvidia_probe()
        };
        let Decision::Disable(reason) = decide(DmabufPolicy::Auto, false, &probe) else {
            panic!("expected the renderer to be disabled");
        };
        assert!(reason.contains("modeset off"), "{reason}");
    }

    #[test]
    fn auto_disables_the_renderer_without_a_render_node() {
        // A container or a VM with no GPU: there is nothing to allocate from,
        // whatever the driver would have been.
        let decision = decide(DmabufPolicy::Auto, false, &Probe::default());
        let Decision::Disable(reason) = decision else {
            panic!("expected the renderer to be disabled");
        };
        assert!(reason.contains("render node"), "{reason}");
    }

    #[test]
    fn auto_leaves_a_mesa_machine_alone() {
        let decision = decide(DmabufPolicy::Auto, false, &mesa_probe());
        assert!(matches!(decision, Decision::Leave(_)), "{decision:?}");
    }

    #[test]
    fn the_file_can_force_it_either_way() {
        assert!(matches!(
            decide(DmabufPolicy::Off, false, &mesa_probe()),
            Decision::Disable(_)
        ));
        assert!(matches!(
            decide(DmabufPolicy::On, false, &nvidia_probe()),
            Decision::Leave(_)
        ));
    }

    #[test]
    fn probes_a_machine_from_its_files() {
        let root = std::env::temp_dir().join(format!("promview-probe-{}", std::process::id()));
        let dri = root.join("dev/dri");
        std::fs::create_dir_all(&dri).unwrap();
        std::fs::write(dri.join("renderD128"), "").unwrap();
        std::fs::write(dri.join("card1"), "").unwrap();
        // Mirrors sysfs: the node directory holds a `device` link to the PCI
        // device, and that is where the driver link lives.
        let device = root.join("sys/class/drm/renderD128/device");
        std::fs::create_dir_all(&device).unwrap();
        std::fs::create_dir_all(root.join("sys/bus/pci/drivers/nvidia")).unwrap();
        std::os::unix::fs::symlink(
            root.join("sys/bus/pci/drivers/nvidia"),
            device.join("driver"),
        )
        .unwrap();
        let parameters = root.join("sys/module/nvidia_drm/parameters");
        std::fs::create_dir_all(&parameters).unwrap();
        std::fs::write(parameters.join("modeset"), "Y\n").unwrap();

        let probe = probe(&root);
        assert_eq!(probe.render_nodes, vec!["renderD128".to_string()]);
        assert!(probe.nvidia);
        assert_eq!(probe.nvidia_modeset, Some(true));

        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn a_machine_with_no_dri_directory_probes_as_empty() {
        let probe = probe(&std::env::temp_dir().join("promview-probe-absent"));
        assert_eq!(probe, Probe::default());
    }
}
