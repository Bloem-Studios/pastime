// Package thirdparty makes distribution notices available inside the executable.
package thirdparty

import _ "embed"

//go:embed NOTICES.txt
var Notices []byte
