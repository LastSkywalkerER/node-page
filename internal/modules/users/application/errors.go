package application

import "errors"

var (
	ErrRegistrationDisabled    = errors.New("registration is disabled")
	ErrEmailExists             = errors.New("email already exists")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrInvalidInvitation       = errors.New("invalid invitation")
	ErrInvitationEmailMismatch = errors.New("invitation email mismatch")
)
