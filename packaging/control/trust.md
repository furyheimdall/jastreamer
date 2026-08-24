# jastreamer Control Windows trust

The Control MSIX uses the jastreamer personal-use Code Signing certificate and has no Windows Public Trust or SmartScreen reputation.

Before installation, compare the SHA-256 digest of `control-windows.cer` with `Windows-CERT-SHA256.txt`. Import that exact certificate into `LocalMachine\TrustedPeople`, install the MSIX, then remove trust when it is no longer needed.
