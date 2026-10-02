package infoblox

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
)

// RequestError adds the Infoblox endpoint and operation to an API request error.
type RequestError struct {
	Endpoint  string
	Operation string
	Params    map[string]string
	Err       error
}

func (e RequestError) Error() string {
	params := make([]string, 0, len(e.Params))
	for _, k := range slices.Sorted(maps.Keys(e.Params)) {
		params = append(params, fmt.Sprintf("%s=%q", k, e.Params[k]))
	}
	kind := ""
	switch {
	case IsWapiError(e.Err):
		kind = "" // WAPI errors already start with the 'WAPI error' prefix.
	case IsTransportError(e.Err):
		kind = "transport error "
	}
	return fmt.Sprintf("infoblox %q %s [%s]: %s%v", e.Endpoint, e.Operation, strings.Join(params, " "), kind, e.Err)
}

func (e RequestError) Unwrap() error {
	return e.Err
}

func newRequestError(err error, endpoint, operation string, params map[string]string) RequestError {
	return RequestError{
		Endpoint:  endpoint,
		Operation: operation,
		Params:    params,
		Err:       parseResponseError(err),
	}
}

// HTTPError is an HTTP error response of the Infoblox API. Without a WapiError around it, the
// response had no WAPI error body, e.g. because a proxy or load balancer sent it.
type HTTPError struct {
	// Err is the raw ibclient error, its message contains the complete response body.
	Err error

	// StatusCode is the HTTP status code of the response, e.g. 502.
	StatusCode int

	// Body is the trimmed response body.
	Body string
}

// maxHTTPErrorBodyLength is the maximum number of bytes of the response body in the message of an HTTPError.
const maxHTTPErrorBodyLength = 200

func (e HTTPError) Error() string {
	msg := fmt.Sprintf("HTTP error %d (%s)", e.StatusCode, http.StatusText(e.StatusCode))
	// ibclient drops the Content-Type header, so the body is sniffed. HTML, e.g. the error page of a proxy, is not shown.
	if e.Body == "" || !strings.HasPrefix(http.DetectContentType([]byte(e.Body)), "text/plain") {
		return msg
	}
	body := strings.Join(strings.Fields(e.Body), " ")
	if len(body) > maxHTTPErrorBodyLength {
		body = strings.ToValidUTF8(body[:maxHTTPErrorBodyLength], "") + " [...]"
	}
	return msg + ": " + body
}

func (e HTTPError) Unwrap() error {
	return e.Err
}

// WapiError is an HTTP error response with a WAPI error body: Infoblox itself rejected the request.
type WapiError struct {
	HTTPError

	Type    string
	Code    string
	Message string
}

func (e WapiError) Error() string {
	return fmt.Sprintf("WAPI error %d %s: %s (error: %q, code: %q)",
		e.StatusCode, http.StatusText(e.StatusCode), e.Message, e.Type, e.Code)
}

func (e WapiError) Unwrap() error {
	return e.HTTPError
}

// IsRequestError reports whether err is a RequestError.
func IsRequestError(err error) bool {
	var reqErr RequestError
	return errors.As(err, &reqErr)
}

// IsWapiError reports whether err is an HTTP error response with a WAPI error body.
func IsWapiError(err error) bool {
	var wapiErr WapiError
	return errors.As(err, &wapiErr)
}

// IsHTTPError reports whether err is an HTTP error response, with or without a WAPI error body.
func IsHTTPError(err error) bool {
	var httpErr HTTPError
	return errors.As(err, &httpErr)
}

// IsAuthError reports whether Infoblox rejected the credentials or their permissions (HTTP 401 or 403).
func IsAuthError(err error) bool {
	var httpErr HTTPError
	return errors.As(err, &httpErr) &&
		(httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden)
}

// IsTransportError reports whether an Infoblox request failed before a WAPI response was received (e.g. DNS, dial, TLS, timeout).
func IsTransportError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}

// wapiConflictCode is the WAPI error code of a full network, but also of other conflicts such as duplicate names.
const wapiConflictCode = "Client.Ibap.Data.Conflict"

// IsNoAddressAvailableError reports whether Infoblox rejected an allocation because the network has no available address.
func IsNoAddressAvailableError(err error) bool {
	var wapiErr WapiError
	return errors.As(err, &wapiErr) &&
		wapiErr.Code == wapiConflictCode &&
		strings.Contains(wapiErr.Message, "available IP address")
}

// IsNotFoundError reports whether err indicates that the requested Infoblox object does not exist.
func IsNotFoundError(err error) bool {
	// ibclient.NotFoundError has a pointer receiver on its Error() method, so the target must be **NotFoundError.
	ibNotFoundErr := &ibclient.NotFoundError{}
	if !errors.As(err, &ibNotFoundErr) {
		return false
	}
	// ibclient turns each HTTP 404 into a NotFoundError. Without a WAPI error body, the 404 did not come from Infoblox.
	parsed := parseResponseError(ibNotFoundErr)
	return IsWapiError(parsed) || !IsHTTPError(parsed)
}

// parseResponseError parses the error text that ibclient builds for an HTTP error response into a
// WapiError, or into an HTTPError if the response has no WAPI error body. Other errors are returned unchanged.
func parseResponseError(in error) error {
	if in == nil {
		return nil
	}
	raw := in.Error()
	status, content, ok := strings.Cut(raw, "Contents:")
	if !ok {
		return in
	}
	// ibclient's format is "WAPI request error: <code>('<status>')\nContents:\n<body>\n".
	codeText, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(status, "WAPI request error:")), "(")
	statusCode, err := strconv.Atoi(codeText)
	if err != nil {
		return in
	}

	type wapiErrorContent struct {
		Error string `json:"Error"`
		Code  string `json:"code"`
		Text  string `json:"text"`
	}
	httpErr := HTTPError{StatusCode: statusCode, Err: in, Body: strings.TrimSpace(content)}
	var wapiErr wapiErrorContent
	err = json.Unmarshal([]byte(content), &wapiErr)
	if err != nil || (wapiErr == (wapiErrorContent{})) {
		return httpErr
	}
	message := wapiErr.Text
	if message == "" {
		message = wapiErr.Error
	}
	return WapiError{
		HTTPError: httpErr,
		Type:      wapiErr.Error,
		Code:      wapiErr.Code,
		Message:   message,
	}
}
