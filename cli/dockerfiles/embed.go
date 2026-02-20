package dockerfiles

import "embed"

//go:embed all:bitcoind all:lnd all:clightning
var FS embed.FS
