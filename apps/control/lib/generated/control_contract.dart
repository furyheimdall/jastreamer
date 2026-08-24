// GENERATED from contracts/control-api/http-api-v1.json. Do not edit by hand.
const controlContractSha256 =
    '2c764806ba98bb8cac8d2b692e07ac830a43efd97552230021230e316cb5ef79';
const controlContractRevision = 'http-api-v1';
const controlProtocolMajor = 2;

const decisionReasonValues = <String>[
  'PLAY_EXPLICIT',
  'PLAY_ALBUM',
  'PLAY_SIMILAR',
  'BLOCK_EXPLICIT',
  'STOP_MODE_OFF',
  'STOP_NO_ALBUM',
  'STOP_ALBUM_COMPLETE',
  'STOP_SIMILAR_NO_SIGNAL',
  'STOP_SIMILAR_EXHAUSTED',
  'STOP_AUTO_FAILURE_LIMIT',
];

const controllerCapabilities = <String>[
  'catalog-status',
  'queue',
  'continuation-policy',
  'automatic-preview',
  'decision-explanation',
  'wss-state',
];
