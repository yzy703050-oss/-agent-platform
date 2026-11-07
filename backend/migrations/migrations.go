package migrations

import "embed"

//go:embed *.sql control/*.sql platform/*.sql travel/*.sql
var Files embed.FS
