package bitbucket

import (
	"errors"
	"fmt"
)

// APIError is an upstream (or transport) failure. Status is the HTTP status code
// returned by Bitbucket, or 0 for transport errors and malformed requests.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return "bitbucket: " + e.Message
	}
	return fmt.Sprintf("bitbucket: %d %s", e.Status, e.Message)
}

// StatusCode returns the upstream status, or 0.
func (e *APIError) StatusCode() int { return e.Status }

// AsAPIError extracts an *APIError from err.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsNotFound reports whether err is an upstream 404.
func IsNotFound(err error) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.Status == 404
}
