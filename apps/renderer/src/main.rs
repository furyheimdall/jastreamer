use clap::Parser;
use jastreamer_renderer::cli::{Cli, RunConfig};
use jastreamer_renderer::engine::Engine;
use jastreamer_renderer::harness::DurableJournal;
use jastreamer_renderer::media::PinnedHttpMediaLoader;
use jastreamer_renderer::session::{SessionConfig, run_foreground};
use std::sync::{Arc, Mutex};

fn main() {
    let arguments = match Cli::try_parse() {
        Ok(value) => value,
        Err(error) => {
            let code = if error.use_stderr() { 64 } else { 0 };
            let _ = error.print();
            std::process::exit(code);
        }
    };
    let result = dispatch(arguments);
    if let Err(error) = result {
        eprintln!("{error}");
        std::process::exit(exit_code(error.as_ref()));
    }
}

fn dispatch(arguments: Cli) -> Result<(), Box<dyn std::error::Error>> {
    if arguments.version {
        println!("jastreamer-renderer {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    if arguments.revision {
        println!("unknown");
        return Ok(());
    }
    if arguments.protocol {
        println!("3 (compatible with 2)");
        return Ok(());
    }
    if arguments.compatibility_fixture.is_some() {
        return jastreamer_renderer::compatibility::run(&arguments)
            .map_err(|error| Box::new(error) as Box<dyn std::error::Error>);
    }
    run(arguments.run_config()?)
}

fn run(config: RunConfig) -> Result<(), Box<dyn std::error::Error>> {
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()?;
    runtime.block_on(run_platform(config))
}

#[cfg(not(windows))]
async fn run_platform(config: RunConfig) -> Result<(), Box<dyn std::error::Error>> {
    if config.output_device != "fixture" {
        return Err("OUTPUT_UNAVAILABLE: WASAPI is available only on Windows".into());
    }
    let certificate = Arc::new(Mutex::new(None));
    let loader = PinnedHttpMediaLoader::new(
        config.server_origin.clone(),
        config.token.clone(),
        Arc::clone(&certificate),
    );
    let journal = DurableJournal::open(&config.state_directory)?;
    let engine = Arc::new(Mutex::new(Engine::new(
        journal,
        jastreamer_renderer::audio::FakePlaybackBackend::default(),
        loader,
    )));
    run_foreground(
        SessionConfig {
            server_origin: &config.server_origin,
            fingerprint: config.server_fingerprint,
            renderer_id: &config.renderer_id,
            token: &config.token,
            observed_certificate: certificate,
        },
        engine,
    )
    .await?;
    Ok(())
}

#[cfg(windows)]
async fn run_platform(config: RunConfig) -> Result<(), Box<dyn std::error::Error>> {
    let certificate = Arc::new(Mutex::new(None));
    let loader = PinnedHttpMediaLoader::new(
        config.server_origin.clone(),
        config.token.clone(),
        Arc::clone(&certificate),
    );
    let journal = DurableJournal::open(&config.state_directory)?;
    let backend = jastreamer_renderer::wasapi::WasapiBackend::new(&config.output_device)?;
    let engine = Arc::new(Mutex::new(Engine::new(journal, backend, loader)));
    run_foreground(
        SessionConfig {
            server_origin: &config.server_origin,
            fingerprint: config.server_fingerprint,
            renderer_id: &config.renderer_id,
            token: &config.token,
            observed_certificate: certificate,
        },
        engine,
    )
    .await?;
    Ok(())
}

fn exit_code(error: &(dyn std::error::Error + 'static)) -> i32 {
    let text = error.to_string();
    if text.contains("UNSUPPORTED_PROTOCOL_MAJOR") {
        78
    } else if text.contains("MISSING_ARGUMENT") || text.contains("INVALID_ARGUMENT") {
        64
    } else {
        65
    }
}
