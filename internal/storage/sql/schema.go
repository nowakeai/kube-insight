package storagesql

import _ "embed"

// Schema is the SQLite bootstrap schema used by local storage.
//
//go:embed schema.sql
var Schema string
