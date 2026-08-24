# jastreamer-renderer Windows release

The MSI installs the Rust/WASAPI renderer; the diagnostic ZIP is portable and contains no installer. WiX is build-only and is never redistributed. Project self-signing is not Windows Public Trust and may show SmartScreen warnings.

Release metadata includes the SHA-256 certificate fingerprint, explicit trust instructions, and certificate removal instructions. Never commit or cache the protected PFX or its password.
