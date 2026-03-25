package apperror

import "net/http"

type AppError struct {
	Code       string
	Message    string
	HTTPStatus int
	Detail     string
	Err        error
}

func (e *AppError) Error() string { return e.Message }
func (e *AppError) Unwrap() error { return e.Err }

func BadRequest(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusBadRequest}
}

func Unauthorized(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusUnauthorized}
}

func Forbidden(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusForbidden}
}

func NotFound(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusNotFound}
}

func Conflict(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusConflict}
}

func Internal(code, message string) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: http.StatusInternalServerError}
}

func Wrap(err error, code, message string, status int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: status, Err: err}
}

func WithDetail(e *AppError, detail string) *AppError {
	e.Detail = detail
	return e
}
