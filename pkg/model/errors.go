/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package model

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorKind distinguishes the error classes a client has to react to differently (CLI-03).
type ErrorKind string

const (
	KindBadRequest  ErrorKind = "bad_request"
	KindValidation  ErrorKind = "validation"
	KindNotFound    ErrorKind = "not_found"
	KindForbidden   ErrorKind = "forbidden"
	KindConflict    ErrorKind = "conflict"
	KindUnavailable ErrorKind = "unavailable"
	KindInternal    ErrorKind = "internal"
	// a limit per user is reached, e.g. live connections (DBG-10)
	KindTooMany ErrorKind = "too_many_requests"
)

// sentinel errors for errors.Is; an *Error matches the sentinel of its kind
var (
	ErrBadRequest  = errors.New(string(KindBadRequest))
	ErrValidation  = errors.New(string(KindValidation))
	ErrNotFound    = errors.New(string(KindNotFound))
	ErrForbidden   = errors.New(string(KindForbidden))
	ErrConflict    = errors.New(string(KindConflict))
	ErrUnavailable = errors.New(string(KindUnavailable))
	ErrInternal    = errors.New(string(KindInternal))
	ErrTooMany     = errors.New(string(KindTooMany))
)

// Issue is a validation finding, bound to a BPMN element where possible (BPMN-18).
type Issue struct {
	ElementId string `json:"element_id,omitempty"`
	Message   string `json:"message"`
}

// Conversion is a model written for Camunda 7 rewritten into the format of this engine
// (MIG-04): what was rewritten, what should be checked by hand, what keeps it from running,
// and the issues of the result under the rules of the namespace.
type Conversion struct {
	Xml         string  `json:"xml"`
	Rewritten   []Issue `json:"rewritten"`
	Check       []Issue `json:"check"`
	Unsupported []Issue `json:"unsupported"`
	Issues      []Issue `json:"issues"`
}

func (this Issue) String() string {
	if this.ElementId == "" {
		return this.Message
	}
	return this.ElementId + ": " + this.Message
}

// Error is the error body of every non-2xx response and the error type the client returns.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
	Issues  []Issue   `json:"issues,omitempty"`
}

func (this *Error) Error() string {
	if len(this.Issues) == 0 {
		return this.Message
	}
	parts := make([]string, 0, len(this.Issues))
	for _, issue := range this.Issues {
		parts = append(parts, issue.String())
	}
	return this.Message + ": " + strings.Join(parts, "; ")
}

func (this *Error) Is(target error) bool {
	return target == this.Kind.sentinel()
}

func (this ErrorKind) sentinel() error {
	switch this {
	case KindBadRequest:
		return ErrBadRequest
	case KindValidation:
		return ErrValidation
	case KindNotFound:
		return ErrNotFound
	case KindForbidden:
		return ErrForbidden
	case KindConflict:
		return ErrConflict
	case KindUnavailable:
		return ErrUnavailable
	case KindTooMany:
		return ErrTooMany
	default:
		return ErrInternal
	}
}

func (this ErrorKind) HttpStatus() int {
	switch this {
	case KindBadRequest, KindValidation:
		return http.StatusBadRequest
	case KindNotFound:
		return http.StatusNotFound
	case KindForbidden:
		return http.StatusForbidden
	case KindConflict:
		return http.StatusConflict
	case KindUnavailable:
		return http.StatusServiceUnavailable
	case KindTooMany:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func KindFromHttpStatus(status int) ErrorKind {
	switch status {
	case http.StatusBadRequest:
		return KindBadRequest
	case http.StatusNotFound:
		return KindNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return KindForbidden
	case http.StatusConflict:
		return KindConflict
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return KindUnavailable
	case http.StatusTooManyRequests:
		return KindTooMany
	default:
		return KindInternal
	}
}

func NewError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func BadRequest(format string, args ...any) *Error {
	return NewError(KindBadRequest, format, args...)
}

func NotFound(format string, args ...any) *Error {
	return NewError(KindNotFound, format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return NewError(KindForbidden, format, args...)
}

func Conflict(format string, args ...any) *Error {
	return NewError(KindConflict, format, args...)
}

func Unavailable(format string, args ...any) *Error {
	return NewError(KindUnavailable, format, args...)
}

func TooMany(format string, args ...any) *Error {
	return NewError(KindTooMany, format, args...)
}

func ValidationFailed(issues []Issue) *Error {
	return &Error{Kind: KindValidation, Message: "validation failed", Issues: issues}
}

// AsError converts any error into an *Error; unknown errors become internal errors.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: KindInternal, Message: err.Error()}
}
