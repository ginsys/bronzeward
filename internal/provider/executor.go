package provider

import "context"

// Executor is the executor identity's provider client (compilation.md §1, persistence-api.md
// §3.3). Its one operation so far is the Talos access read its observations use; artifact
// decryption arrives with dispatch (TestExecutorMethodSet).
type Executor struct{ c *client }

// NewExecutor builds the client without contacting the provider.
func NewExecutor(addr string, tok Token) (*Executor, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Executor{c: c}, nil
}

// TalosAccess reads the latest version of cluster's Talos credential, as Ingestion.TalosAccess
// does, under the executor's token.
func (e *Executor) TalosAccess(ctx context.Context, cluster string) (TalosAccess, error) {
	return readTalosAccess(ctx, e.c, cluster)
}
