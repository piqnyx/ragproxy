package main

import (
	"bytes"
	"net/http"
)

// responseCapture captures the response for logging
type responseCapture struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
}

// WriteHeader captures the status code
func (rc *responseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

// Write captures the response body
func (rc *responseCapture) Write(data []byte) (int, error) {
	rc.body.Write(data)
	return rc.ResponseWriter.Write(data)
}
