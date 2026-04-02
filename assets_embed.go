package miot

import "embed"

// embeddedAssets stores bundled MIoT translation, spec, and LAN profile data.
//
//go:embed i18n/*.json specs/* lan/profile_models.yaml
var embeddedAssets embed.FS
