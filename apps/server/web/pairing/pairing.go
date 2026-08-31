package pairing

import "embed"

// Assets contains the Server-owned pairing administration portal.
//
//go:embed index.html style.css app.js favicon.svg
var Assets embed.FS
