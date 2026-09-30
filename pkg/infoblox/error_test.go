package infoblox

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/gomega"
)

// rawWapiError builds an error the way ibclient reports a failed HTTP response.
func rawWapiError(status int, statusText, contents string) error {
	return fmt.Errorf("WAPI request error: %d('%d %s')\nContents:\n%s\n", status, status, statusText, contents) //nolint:revive // mirrors ibclient's message format
}

const wapiErrorContents = `{ "Error": "AdmConProtoError: Field is not searchable: foo",
  "code": "Client.Ibap.Proto",
  "text": "Field is not searchable: foo"
}`

func TestParseResponseErrorReturnsUnparseableErrorsUnchanged(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "no contents section", err: errors.New("dial tcp: connection refused")},
		{name: "status code is not a number", err: errors.New("WAPI request error: abc('abc')\nContents:\n{}\n")}, //nolint:revive // mirrors ibclient's message format
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(parseResponseError(tt.err)).To(BeIdenticalTo(tt.err))
		})
	}
}

func TestParseResponseErrorReturnsNilForNil(t *testing.T) {
	g := NewWithT(t)

	g.Expect(parseResponseError(nil)).To(Succeed())
}

func TestParseResponseErrorReturnsHTTPErrorWithoutWapiBody(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{name: "contents are no JSON", err: rawWapiError(502, "Bad Gateway", "<html>bad gateway</html>"), code: 502},
		{name: "contents have no error fields", err: rawWapiError(500, "Internal Server Error", `{"foo": "bar"}`), code: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := parseResponseError(tt.err)

			var httpErr HTTPError
			g.Expect(errors.As(err, &httpErr)).To(BeTrue())
			g.Expect(httpErr.StatusCode).To(Equal(tt.code))
			g.Expect(IsWapiError(err)).To(BeFalse())
			g.Expect(errors.Is(err, tt.err)).To(BeTrue())
			g.Expect(err.Error()).NotTo(ContainSubstring("Contents"), "the raw ibclient error must not be part of the message")
		})
	}
}

func TestHTTPErrorMessage(t *testing.T) {
	longBody := strings.Repeat("a", 151) + strings.Repeat("ä", 100)
	tests := []struct {
		name       string
		status     int
		statusText string
		contents   string
		want       string
	}{
		{
			name:   "plain text body",
			status: 400, statusText: "Bad Request", contents: "Version 9.9 not supported",
			want: "HTTP error 400 (Bad Request): Version 9.9 not supported",
		},
		{
			name:   "HTML body is not shown",
			status: 401, statusText: "Unauthorized", contents: "\n  <!DOCTYPE html><html><body>Authorization Required</body></html>",
			want: "HTTP error 401 (Unauthorized)",
		},
		{
			name:   "XML body is not shown",
			status: 502, statusText: "Bad Gateway", contents: `<?xml version="1.0"?><error>bad gateway</error>`,
			want: "HTTP error 502 (Bad Gateway)",
		},
		{
			name:   "plain text body that starts with <",
			status: 400, statusText: "Bad Request", contents: "<none> is not a valid version",
			want: "HTTP error 400 (Bad Request): <none> is not a valid version",
		},
		{
			name:   "empty body",
			status: 503, statusText: "Service Unavailable", contents: "  \n",
			want: "HTTP error 503 (Service Unavailable)",
		},
		{
			name:   "JSON body without WAPI error fields",
			status: 500, statusText: "Internal Server Error", contents: `{"foo": "bar"}`,
			want: `HTTP error 500 (Internal Server Error): {"foo": "bar"}`,
		},
		{
			name:   "line breaks become spaces",
			status: 502, statusText: "Bad Gateway", contents: "upstream\r\nconnect error\n\nreset",
			want: "HTTP error 502 (Bad Gateway): upstream connect error reset",
		},
		{
			name:   "long body is cut after 200 bytes without a partial character",
			status: 502, statusText: "Bad Gateway", contents: longBody,
			want: "HTTP error 502 (Bad Gateway): " + strings.Repeat("a", 151) + strings.Repeat("ä", 24) + " [...]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := parseResponseError(rawWapiError(tt.status, tt.statusText, tt.contents))

			g.Expect(err).To(MatchError(tt.want))
		})
	}
}

func TestParseResponseErrorExtractsResponseDetails(t *testing.T) {
	g := NewWithT(t)
	raw := rawWapiError(400, "Bad Request", wapiErrorContents)

	err := parseResponseError(raw)

	var wapiErr WapiError
	g.Expect(errors.As(err, &wapiErr)).To(BeTrue())
	g.Expect(wapiErr.StatusCode).To(Equal(400))
	g.Expect(IsHTTPError(err)).To(BeTrue(), "a WAPI error is an HTTP error response")
	g.Expect(wapiErr.Type).To(Equal("AdmConProtoError: Field is not searchable: foo"))
	g.Expect(wapiErr.Code).To(Equal("Client.Ibap.Proto"))
	g.Expect(wapiErr.Message).To(Equal("Field is not searchable: foo"))
	g.Expect(errors.Is(err, raw)).To(BeTrue())
	g.Expect(err).To(MatchError(`WAPI error 400 Bad Request: Field is not searchable: foo ` +
		`(error: "AdmConProtoError: Field is not searchable: foo", code: "Client.Ibap.Proto")`))
}

func TestParseResponseErrorFallsBackToErrorFieldWithoutText(t *testing.T) {
	g := NewWithT(t)
	raw := rawWapiError(401, "Unauthorized", `{"Error": "AdmConAuthError: Authorization failed"}`)

	err := parseResponseError(raw)

	g.Expect(err).To(MatchError(`WAPI error 401 Unauthorized: AdmConAuthError: Authorization failed ` +
		`(error: "AdmConAuthError: Authorization failed", code: "")`))
}

func TestParseResponseErrorKeepsNotFoundErrorsDetectable(t *testing.T) {
	g := NewWithT(t)
	raw := ibclient.NewNotFoundError(rawWapiError(404, "Not Found", wapiErrorContents).Error())

	err := parseResponseError(raw)

	g.Expect(err).To(BeAssignableToTypeOf(WapiError{}))
	g.Expect(IsNotFoundError(err)).To(BeTrue())
}

func TestRequestErrorMessageContainsSortedParams(t *testing.T) {
	g := NewWithT(t)

	err := newRequestError(errors.New("boom"), "ib.example.com:443", "GetNetwork", map[string]string{
		"view":   "my-view",
		"subnet": "10.0.0.0/24",
	})

	g.Expect(err).To(MatchError(`infoblox "ib.example.com:443" GetNetwork [subnet="10.0.0.0/24" view="my-view"]: boom`))
}

func TestRequestErrorMessageWithoutParams(t *testing.T) {
	g := NewWithT(t)

	err := newRequestError(errors.New("boom"), "ib.example.com:443", "DeleteHostRecord", nil)

	g.Expect(err).To(MatchError(`infoblox "ib.example.com:443" DeleteHostRecord []: boom`))
}

func TestRequestErrorMessageMarksWapiErrors(t *testing.T) {
	g := NewWithT(t)
	raw := rawWapiError(401, "Unauthorized", `{"Error": "AdmConAuthError", "code": "Client.Ibap.Auth", "text": "Authorization failed"}`)

	err := newRequestError(raw, "ib.example.com:443", "GetDNSView", nil)

	g.Expect(err).To(MatchError(`infoblox "ib.example.com:443" GetDNSView []: WAPI error 401 Unauthorized: ` +
		`Authorization failed (error: "AdmConAuthError", code: "Client.Ibap.Auth")`))
}

func TestRequestErrorUnwrapsToParsedWapiError(t *testing.T) {
	g := NewWithT(t)
	raw := rawWapiError(401, "Unauthorized", `{"Error": "AdmConAuthError", "code": "Client.Ibap.Auth", "text": "Authorization failed"}`)

	err := fmt.Errorf("outer: %w", newRequestError(raw, "ib.example.com:443", "GetDNSView", nil))

	var reqErr RequestError
	g.Expect(errors.As(err, &reqErr)).To(BeTrue())
	g.Expect(reqErr.Operation).To(Equal("GetDNSView"))
	var wapiErr WapiError
	g.Expect(errors.As(err, &wapiErr)).To(BeTrue())
	g.Expect(wapiErr.Message).To(Equal("Authorization failed"))
	g.Expect(errors.Is(err, raw)).To(BeTrue())
}

func TestRequestErrorMessageMarksTransportErrors(t *testing.T) {
	g := NewWithT(t)
	dialErr := &url.Error{Op: "Get", URL: "https://ib.example.com/wapi", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}

	err := newRequestError(dialErr, "ib.example.com:443", "GetNetwork", nil)

	g.Expect(err).To(MatchError(`infoblox "ib.example.com:443" GetNetwork []: transport error ` + dialErr.Error()))
}

func TestIsTransportError(t *testing.T) {
	dnsErr := &url.Error{Op: "Get", URL: "https://ib.example.com/wapi", Err: &net.DNSError{Err: "no such host", Name: "ib.example.com"}}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "url error of the HTTP client", err: dnsErr, want: true},
		{name: "transport error inside a request error", err: newRequestError(dnsErr, "host:443", "Op", nil), want: true},
		{name: "WAPI error", err: newRequestError(rawWapiError(401, "Unauthorized", wapiErrorContents), "host:443", "Op", nil), want: false},
		{name: "HTTP error without WAPI body", err: rawWapiError(502, "Bad Gateway", "<html>bad gateway</html>"), want: false},
		{name: "not found error", err: ibclient.NewNotFoundError("not found"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(IsTransportError(tt.err)).To(Equal(tt.want))
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	notFound := ibclient.NewNotFoundError("not found")
	wapiNotFound := ibclient.NewNotFoundError(rawWapiError(404, "Not Found", wapiErrorContents).Error())
	proxyNotFound := ibclient.NewNotFoundError(rawWapiError(404, "Not Found", "<html>not found</html>").Error())
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "typed not found error", err: notFound, want: true},
		{name: "wrapped not found error", err: fmt.Errorf("lookup: %w", notFound), want: true},
		{name: "not found error inside a request error", err: newRequestError(notFound, "host:443", "Op", nil), want: true},
		{name: "404 with WAPI body", err: wapiNotFound, want: true},
		{name: "404 with WAPI body inside a request error", err: newRequestError(wapiNotFound, "host:443", "Op", nil), want: true},
		{name: "404 without WAPI body", err: proxyNotFound, want: false},
		{name: "404 without WAPI body inside a request error", err: newRequestError(proxyNotFound, "host:443", "Op", nil), want: false},
		{name: "untyped error ending in not found", err: errors.New("network view 'x' not found"), want: false},
		{name: "other error", err: errors.New("boom"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(IsNotFoundError(tt.err)).To(Equal(tt.want))
		})
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "WAPI 401", err: newRequestError(rawWapiError(401, "Unauthorized", wapiErrorContents), "host:443", "Op", nil), want: true},
		{name: "HTTP 401 without WAPI body", err: newRequestError(rawWapiError(401, "Unauthorized", "<html>401</html>"), "host:443", "Op", nil), want: true},
		{name: "WAPI 403", err: newRequestError(rawWapiError(403, "Forbidden", wapiErrorContents), "host:443", "Op", nil), want: true},
		{name: "WAPI 400", err: newRequestError(rawWapiError(400, "Bad Request", wapiErrorContents), "host:443", "Op", nil), want: false},
		{name: "unparsed 401", err: rawWapiError(401, "Unauthorized", wapiErrorContents), want: false},
		{name: "other error", err: errors.New("boom"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(IsAuthError(tt.err)).To(Equal(tt.want))
		})
	}
}
