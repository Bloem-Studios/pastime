// Package fault defines safe public errors. Database/credential details never become HTTP messages.
package fault

import "errors"

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Code }

var (
	Unauthenticated   = &Error{401, "unauthenticated", "Sign in to continue."}
	Forbidden         = &Error{403, "forbidden", "You do not have permission to do that."}
	NotFound          = &Error{404, "not_found", "This record was not found."}
	Conflict          = &Error{409, "conflict", "This record changed or already exists. Refresh and try again."}
	LastOwner         = &Error{409, "last_owner", "Keep at least one active owner."}
	UnavailableInvite = &Error{404, "invitation_unavailable", "This invitation is unavailable. Ask for a new link."}
	Limited           = &Error{429, "rate_limited", "Too many attempts. Please wait a minute and try again."}
	Unavailable       = &Error{503, "unavailable", "The service is temporarily unavailable."}
	Internal          = &Error{500, "internal_error", "The request could not be completed."}
	Gone              = &Error{410, "gone", "This record has been removed."}
)

func Invalid(message string) error { return &Error{422, "validation_error", message} }
func Public(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal
}
