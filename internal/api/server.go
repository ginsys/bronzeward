package api

import (
	"context"
	"database/sql"
	"net/http"
	"runtime/debug"
	"time"
)

// apiRevision is the API revision within v1 (persistence-api.md §9.1): raised by one with each
// additive change, which adds its row to §9.1's revision table.
const apiRevision = 3

// version is the server's version when the build sets it
// (-ldflags "-X github.com/ginsys/bronzeward/internal/api.version=…"); otherwise serverVersion
// reads the main module's version from the build information.
var version string

// Version is what GET /server reports as this binary's version.
func Version() string { return serverVersion() }

func serverVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// serverBody is GET /server (persistence-api.md §9.2): what this process serves and the read-only
// settings a client shows. It describes the process that answered; another instance may differ.
type serverBody struct {
	Version                     string           `json:"version"`
	APIRevision                 int              `json:"apiRevision"`
	Features                    []string         `json:"features"`
	StartMode                   string           `json:"startMode"`
	MaxTokenLifetimeSeconds     float64          `json:"maxTokenLifetimeSeconds"`
	SettleFloorSeconds          float64          `json:"settleFloorSeconds"`
	MaxTransportDeadlineSeconds float64          `json:"maxTransportDeadlineSeconds"`
	IngestionClaim              *ingestionClaim  `json:"ingestionClaim"`
	PlanDefaults                planDefaultsBody `json:"planDefaults"`
}

type ingestionClaim struct {
	LeaseSeconds          float64 `json:"leaseSeconds"`
	AbsoluteExpirySeconds float64 `json:"absoluteExpirySeconds"`
}

// planDefaultsBody names each default as a plan creation request names the value (plans.go).
type planDefaultsBody struct {
	ExpiresInSeconds            float64 `json:"expiresInSeconds"`
	MaxObservationAgeSeconds    float64 `json:"maxObservationAgeSeconds"`
	CheckValiditySeconds        float64 `json:"checkValiditySeconds"`
	TransportDeadlineSeconds    float64 `json:"transportDeadlineSeconds"`
	VerificationDeadlineSeconds float64 `json:"verificationDeadlineSeconds"`
	MaxAttempts                 int     `json:"maxAttempts"`
}

func getServer(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(context.Context, *sql.Tx) (string, any, error) {
		if q.r.URL.RawQuery != "" {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "the server read takes no query")
		}
		ex, pd := a.d.exec, a.d.exec.PlanDefaults
		b := serverBody{Version: serverVersion(), APIRevision: apiRevision, Features: []string{}, StartMode: "normal",
			MaxTokenLifetimeSeconds: a.lifetime.Seconds(), SettleFloorSeconds: ex.SettleFloor.Seconds(),
			MaxTransportDeadlineSeconds: ex.MaxTransportDeadline.Seconds(),
			PlanDefaults: planDefaultsBody{ExpiresInSeconds: pd.Expiry.Seconds(), MaxObservationAgeSeconds: pd.MaxObservationAge.Seconds(),
				CheckValiditySeconds: pd.CheckValidity.Seconds(), TransportDeadlineSeconds: pd.TransportDeadline.Seconds(),
				VerificationDeadlineSeconds: pd.VerificationDeadline.Seconds(), MaxAttempts: pd.MaxAttempts}}
		// In identifier order; without a provider all three clients are nil (deps).
		if a.d.exe != nil {
			b.Features = append(b.Features, "execution")
		}
		if a.d.ing != nil {
			b.Features = append(b.Features, "ingestion")
		}
		if a.d.pub != nil {
			b.Features = append(b.Features, "publication")
		}
		if a.d.recoveryStart {
			b.StartMode = "recovery"
		}
		if t := a.d.timers; t.Lease != 0 {
			b.IngestionClaim = &ingestionClaim{LeaseSeconds: seconds(t.Lease), AbsoluteExpirySeconds: seconds(t.AbsoluteExpiry)}
		}
		return "", b, nil
	})
}

// seconds is d in seconds, exact to the microsecond the claim timers are kept to (config).
func seconds(d time.Duration) float64 { return float64(d/time.Microsecond) / 1e6 }
