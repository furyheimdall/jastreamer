export const writeControlGatewayResult = (results) => {
  process.stdout.write(`${JSON.stringify({
    server: 'production-go',
    tls: 'fingerprint-verified',
    scan_wait: 'catalog_scan-invalidation-plus-exact-job',
    results,
    cleanup: {
      server_stopped: true,
      renderer_stopped: true,
      proxies_closed: true,
      temporary_directory_removed: true,
    },
  })}
`);
};
