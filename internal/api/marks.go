package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// markInput is a mark's body (§9.3): 1 to maxMarks compilation §2.2 paths. A path can spell an
// extracted value, so it is never quoted back: one that does not parse is named by its position.
type markInput struct {
	Marks []string `json:"marks"`
	marks []ingest.Path
}

func (in *markInput) check(*API) error {
	if len(in.Marks) == 0 || len(in.Marks) > maxMarks {
		return fmt.Errorf("marks must hold 1 to %d paths", maxMarks)
	}
	in.marks = make([]ingest.Path, len(in.Marks))
	for i, s := range in.Marks {
		p, err := ingest.ParsePath(s)
		if err != nil {
			return refuse(http.StatusUnprocessableEntity, "validation-failed", "a mark is not a compilation §2.2 path").
				with("position", i)
		}
		in.marks[i] = p
	}
	return nil
}

// keyedDigest covers the marks, which are the request's unextracted input (§7.1).
func (in *markInput) keyedDigest(ctx context.Context, h ingest.HMAC, material []byte, version int) (provider.Digest, error) {
	return ingest.FingerprintMarks(ctx, h, material, in.Marks, version)
}
