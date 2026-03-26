package application

import "errors"

var (
	ErrAuthSecretsNotConfigured = errors.New("authentication secrets are not configured")
	ErrRegistrationDisabled     = errors.New("registration is disabled")
	ErrEmailExists             = errors.New("email already exists")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrInvalidInvitation       = errors.New("invalid invitation")
	ErrInvitationEmailMismatch = errors.New("invitation email mismatch")
)
