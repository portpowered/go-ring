package requestauth

import "context"

// Account is the authorization snapshot for one operation. It is never stored
// on a shared transport client.
type Account struct {
	AccessToken string
	HardwareID  string
}

type contextKey struct{}

func WithAccount(ctx context.Context, account Account) context.Context {
	return context.WithValue(ctx, contextKey{}, account)
}

func FromContext(ctx context.Context) (Account, bool) {
	account, ok := ctx.Value(contextKey{}).(Account)
	return account, ok
}
