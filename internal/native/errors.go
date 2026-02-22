package native

import (
	"errors"
	"fmt"

	"github.com/marcohefti/zero-context-lab/internal/codes"
)

type ErrorKind string

const (
	ErrorUnsupportedStrategy   ErrorKind = "unsupported_strategy"
	ErrorStrategyUnavailable   ErrorKind = "strategy_unavailable"
	ErrorCapabilityUnsupported ErrorKind = "capability_unsupported"
	ErrorCompatibility         ErrorKind = "compatibility"
	ErrorStartup               ErrorKind = "startup"
	ErrorTransport             ErrorKind = "transport"
	ErrorProtocol              ErrorKind = "protocol"
	ErrorTimeout               ErrorKind = "timeout"
	ErrorStreamDisconnect      ErrorKind = "stream_disconnect"
	ErrorEnvPolicy             ErrorKind = "env_policy"
	ErrorAuth                  ErrorKind = "auth"
	ErrorRateLimit             ErrorKind = "rate_limit"
	ErrorListenerFailure       ErrorKind = "listener_failure"
	ErrorCrash                 ErrorKind = "crash"
)

type StrategyFailure struct {
	Strategy StrategyID `json:"strategy"`
	Code     string     `json:"code"`
	Message  string     `json:"message"`
}

type Error struct {
	Code       string            `json:"code"`
	Kind       ErrorKind         `json:"kind"`
	Strategy   StrategyID        `json:"strategy,omitempty"`
	Message    string            `json:"message"`
	Retryable  bool              `json:"retryable,omitempty"`
	Failures   []StrategyFailure `json:"failures,omitempty"`
	Underlying error             `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "native runtime error"
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Underlying
}

func ErrorCodeForKind(kind ErrorKind) string {
	switch kind {
	case ErrorUnsupportedStrategy:
		return codes.RuntimeStrategyUnsupported
	case ErrorStrategyUnavailable:
		return codes.RuntimeStrategyUnavailable
	case ErrorCapabilityUnsupported:
		return codes.RuntimeCapabilityUnsupported
	case ErrorCompatibility:
		return codes.RuntimeCompatibility
	case ErrorStartup:
		return codes.RuntimeStartup
	case ErrorTransport:
		return codes.RuntimeTransport
	case ErrorProtocol:
		return codes.RuntimeProtocol
	case ErrorTimeout:
		return codes.RuntimeTimeout
	case ErrorStreamDisconnect:
		return codes.RuntimeStreamDisconnect
	case ErrorEnvPolicy:
		return codes.RuntimeEnvPolicy
	case ErrorAuth:
		return codes.RuntimeAuth
	case ErrorRateLimit:
		return codes.RuntimeRateLimit
	case ErrorListenerFailure:
		return codes.RuntimeListenerFailure
	case ErrorCrash:
		return codes.RuntimeCrash
	default:
		return codes.RuntimeProtocol
	}
}

func NewError(kind ErrorKind, message string) *Error {
	return &Error{Code: ErrorCodeForKind(kind), Kind: kind, Message: message}
}

func WrapError(kind ErrorKind, message string, err error) *Error {
	if err == nil {
		return NewError(kind, message)
	}
	return &Error{Code: ErrorCodeForKind(kind), Kind: kind, Message: message, Underlying: err}
}

func AsError(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
