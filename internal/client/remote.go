package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
)

// Errors toward the identity provider and the client-configuration host show only what
// bronzewardctl itself knows, a status code with Go's standard text, an error code from oauthCodes,
// and a transport cause named by its type (client-contracts.md §6 item 4). Nothing else that side
// sent is shown, since it may hold a token the provider issued: not a body, an error_description, a
// header, a redirect target beyond its scheme and host, a reason phrase, nor a value a library
// decoded from the answer. The libraries quote all of these in their errors, so no library error's
// own text is shown on these paths, except the few named in why.

// oauthCodes are the error codes shown: RFC 6749 §4.1.2.1 and §5.2, RFC 7009 §2.2.1, RFC 8628
// §3.5 and OpenID Connect Core 1.0 §3.1.2.6. A provider may send any other text as its code.
var oauthCodes = map[string]bool{
	"invalid_request": true, "invalid_client": true, "invalid_grant": true, "unauthorized_client": true,
	"unsupported_grant_type": true, "invalid_scope": true, "access_denied": true, "unsupported_response_type": true,
	"server_error": true, "temporarily_unavailable": true, "unsupported_token_type": true,
	"authorization_pending": true, "slow_down": true, "expired_token": true,
	"interaction_required": true, "login_required": true, "account_selection_required": true, "consent_required": true,
	"invalid_request_uri": true, "invalid_request_object": true, "request_not_supported": true,
	"request_uri_not_supported": true, "registration_not_supported": true,
}

// refusal is a provider's refusal as shown: the status code and its standard text, and the error
// code when oauthCodes holds it.
func refusal(status int, code string) string {
	s := fmt.Sprintf("%d %s", status, http.StatusText(status))
	if oauthCodes[code] {
		s += " " + code
	}
	return s
}

// refusedRedirect is a redirect issuerClient does not follow. net/http puts the Location header,
// as the provider sent it, in the url.Error it returns, so the refusal is shown by this error alone,
// which names only the target's scheme and host.
type refusedRedirect struct {
	to   *url.URL
	many bool
}

func (r *refusedRedirect) Error() string {
	if r.many {
		return "it redirected more than 10 times, which bronzewardctl does not follow"
	}
	return fmt.Sprintf("it redirected to %s, which bronzewardctl does not follow", printable(r.to.Scheme+"://"+r.to.Host))
}

// remoteFailure is a failed request to the identity provider or the client-configuration host, as
// shown: by why, never by err's own text. errors.Is and errors.As still find what err wraps.
func remoteFailure(err error) error {
	return &remote{why(err, "the request failed"), err}
}

type remote struct {
	text string
	err  error
}

func (e *remote) Error() string { return e.text }
func (e *remote) Unwrap() error { return e.err }

// why names err's cause by its type, searched through every wrapper, or else is otherwise. The only
// library text it shows is a certificate's, as certReason gives it, and a DNS failure's name, the
// host bronzewardctl asked for.
func why(err error, otherwise string) string {
	var (
		rr *refusedRedirect
		tr *trust
		cv *tls.CertificateVerificationError
		rh tls.RecordHeaderError
		de *net.DNSError
		oe *net.OpError
		ne net.Error
	)
	switch {
	case errors.As(err, &rr):
		return rr.Error()
	case errors.As(err, &tr):
		return tr.Error() // a proxy's refused certificate, by fields this client chose (newHTTPClient)
	case errors.As(err, &cv):
		return "its certificate is not trusted: " + certReason(cv.Err)
	case errors.As(err, &rh):
		return "it does not answer TLS"
	case errors.As(err, &oe) && oe.Op == "remote error":
		return "it sent a TLS alert"
	case errors.Is(err, context.Canceled):
		return "it was cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "it timed out"
	case errors.As(err, &de):
		return fmt.Sprintf("the name %s could not be resolved", printable(de.Name))
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the connection was refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "the connection was reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the connection closed before an answer"
	}
	return otherwise
}

// certReason is why a certificate was refused. A host name mismatch lists the names the
// certificate holds, made printable, as §6 item 4 shows them; any other reason is a fixed text, since
// x509 quotes other certificate contents in its errors.
func certReason(err error) string {
	var (
		he x509.HostnameError
		ua x509.UnknownAuthorityError
		ci x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &he):
		return printable(he.Error())
	case errors.As(err, &ua):
		return "x509: certificate signed by unknown authority"
	case errors.As(err, &ci) && ci.Reason == x509.Expired:
		return "x509: certificate has expired or is not yet valid"
	}
	return "x509: certificate is not valid"
}
