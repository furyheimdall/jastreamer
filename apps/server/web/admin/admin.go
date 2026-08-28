package admin

import "embed"

// Assets contains the Server-owned administration application.
//
//go:embed index.html style.css api.js app.js
var Assets embed.FS
