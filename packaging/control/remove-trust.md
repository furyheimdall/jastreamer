# Remove jastreamer Control trust

Remove the installed Control package first. Then remove only the certificate whose SHA-256 digest matches `Windows-CERT-SHA256.txt` from `LocalMachine\TrustedPeople`. Do not remove unrelated certificates.
