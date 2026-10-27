// Package patches embeds the PHP source patches the builds apply.
package patches

import "embed"

//go:embed php-*
var FS embed.FS
