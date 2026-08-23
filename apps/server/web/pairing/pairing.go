package pairing

import "embed"

// Assets contains the Server-owned pairing administration portal.
//
//go:embed index.html style.css app.js
var Assets embed.FS
