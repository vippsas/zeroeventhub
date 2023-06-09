package zeroeventhub

import (
	"net/http"
)

// StatusError represents HTTP-friendly error (message + HTTP code).
type StatusError interface {
	error
	Status() int
}

// APIError implements StatusError interface.
type APIError struct {
	message string
	code    int
}

// NewAPIError is a constructor for APIError.
func NewAPIError(message string, code int) *APIError {
	return &APIError{message: message, code: code}
}

func (ae APIError) Error() string {
	return ae.message
}

func (ae APIError) Status() int {
	return ae.code
}

var (
	ErrHandshakePartitionCountMissing  = NewAPIError("handshake error: partition count missing", http.StatusBadRequest)
	ErrHandshakePartitionCountMismatch = NewAPIError("handshake error: partition count mismatch", http.StatusBadRequest)
	ErrCursorsMissing                  = NewAPIError("cursors are missing", http.StatusBadRequest)
	ErrPartitionDoesntExist            = NewAPIError("partition doesn't exist", http.StatusBadRequest)
	ErrIllegalToken                    = NewAPIError("illegal token, please fetch new from discovery endpoint", http.StatusConflict)
)
