package identity

import "github.com/Bloem-Studios/pastime/internal/fault"

// ErrServiceAccessRequired explains a configured paid-deployment admission rule.
var ErrServiceAccessRequired = &fault.Error{Status: 403, Code: "service_access_required", Message: "An active service subscription is required to use Pastime."}
