package dockerfiles

import "embed"

//go:embed all:bitcoind all:lnd all:clightning all:ldk-server all:eclair
var FS embed.FS
