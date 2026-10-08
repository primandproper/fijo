// Package principal is who is calling, as every gRPC handler in this process
// reads it: the identity principal the authentication interceptor resolved,
// the grants the policy gave them, and the session their token named.
//
// It is one package rather than a field on each server because a deployment
// has one notion of a caller. platform-go's surfaces read a callers.Principal
// and an authorization.Grants off the context; Fijo's own read the same two
// values plus the session, so a request cannot be one caller to an adopted
// surface and another to a local one.
package principal

import (
	"context"

	"github.com/primandproper/platform-go/v15/callers"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// Caller is everything the interceptor established about a request.
type Caller struct {
	_         struct{} `json:"-"`
	Principal *identity.Principal
	SessionID string
	Grants    authorization.Grants
}

var _ callers.Principal = (*Caller)(nil)

// UserID is the calling user.
func (c *Caller) UserID() string { return c.Principal.User.ID }

// Scope is whose directory the caller is in, which is always the global one.
//
// Fijo has a single user directory: a studio is an account inside it rather
// than a directory of its own, so a person who teaches at two studios is one
// user with two memberships. The studio — Fijo's tenant — is ActiveAccountID,
// and StudioScope is how a Fijo row reads it. Conflating the two would scope
// the directory by account, which makes a user invisible to the studio they
// have not selected.
func (*Caller) Scope() tenancy.Scope { return tenancy.Global() }

// ActiveAccountID is the studio this request is against, or empty when the
// caller belongs to none.
func (c *Caller) ActiveAccountID() string { return c.Principal.ActiveAccountID }

// StudioScope is the tenancy every Fijo row carries: the active studio, or the
// global scope for a caller with no studio, which is the operator.
func (c *Caller) StudioScope() tenancy.Scope {
	if c.Principal.ActiveAccountID == "" {
		return tenancy.Global()
	}

	return tenancy.Of(c.Principal.ActiveAccountID)
}

type contextKey struct{}

// With returns a context carrying the caller.
func With(ctx context.Context, caller *Caller) context.Context {
	return context.WithValue(ctx, contextKey{}, caller)
}

// FromContext returns the caller the interceptor established, or false on a
// request that carried no token.
func FromContext(ctx context.Context) (*Caller, bool) {
	caller, ok := ctx.Value(contextKey{}).(*Caller)
	if !ok || caller == nil || caller.Principal == nil || caller.Principal.User == nil {
		return nil, false
	}

	return caller, true
}

// Extract is the callers.PrincipalExtractor every platform surface reads.
func Extract(ctx context.Context) (callers.Principal, bool) {
	caller, ok := FromContext(ctx)
	if !ok {
		return nil, false
	}

	return caller, true
}

// Grants is the authorization.GrantsExtractor the enforcer reads. A request
// with no caller grants nothing, which is the safe direction: a write gated on
// a grant is refused, and a read that would have been narrowed by one is
// narrowed.
func Grants(ctx context.Context) (authorization.Grants, bool) {
	caller, ok := FromContext(ctx)
	if !ok {
		return authorization.DenyAll(), false
	}

	return caller.Grants, true
}
