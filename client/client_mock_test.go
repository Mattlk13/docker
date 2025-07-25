package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/moby/moby/api/types"
)

// transportFunc allows us to inject a mock transport for testing. We define it
// here so we can detect the tlsconfig and return nil for only this type.
type transportFunc func(*http.Request) (*http.Response, error)

func (tf transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return tf(req)
}

func transportEnsureBody(f transportFunc) transportFunc {
	return func(req *http.Request) (*http.Response, error) {
		resp, err := f(req)
		if resp != nil && resp.Body == nil {
			resp.Body = http.NoBody
		}
		return resp, err
	}
}

func newMockClient(doer func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{
		// Some tests return a response with a nil body, this is incorrect semantically and causes a panic with wrapper transports (such as otelhttp's)
		// Wrap the doer to ensure a body is always present even if it is empty.
		Transport: transportEnsureBody(transportFunc(doer)),
	}
}

func errorMock(statusCode int, message string) func(req *http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		header := http.Header{}
		header.Set("Content-Type", "application/json")

		body, err := json.Marshal(&types.ErrorResponse{
			Message: message,
		})
		if err != nil {
			return nil, err
		}

		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     header,
		}, nil
	}
}

func plainTextErrorMock(statusCode int, message string) func(req *http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewReader([]byte(message))),
		}, nil
	}
}
