// Package notices embeds THIRD_PARTY_NOTICES.md so the binary ships it
// (`piste --licenses`).
package notices

import _ "embed"

// Text is cli/THIRD_PARTY_NOTICES.md.
//
//go:embed THIRD_PARTY_NOTICES.md
var Text string
