// GENERATED from contracts/control-api/v3/http-api.json. Do not edit by hand.
const controlContractSha256 =
    'b5c3bb56f4d101011b5d99c6abcc88429ee150ed56272d763996094e683843db';
const controlContractRevision = 'control-api-v3';
const controlProtocolMajor = 3;

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
  'control-api',
  'render',
  'catalog-browse',
  'queue-mutation',
  'transport',
  'zones',
  'renderer-assignment',
  'event-invalidations',
  'renderer-session',
  'media-representations',
];
