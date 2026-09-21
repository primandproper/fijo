// Package session is the server-side record behind every Fijo access token.
//
// platform-go's signin service mints tokens and refresh-token families, and
// its documentation leaves the revocation record to the consumer: a token
// carries the family in its "sid" claim so that an interceptor can check it
// against something, and the something is this. Every mint — a sign-in or a
// refresh exchange — creates one sessions row through the claims builder and
// writes its identifier into the token as ClaimSessionID; the interceptor
// reads the row back on every request, which is what makes signing out take
// effect on the next call rather than when the token expires.
//
// One row per mint rather than one per family, because the builder is handed
// the family and nothing else, and sessions.Store has no lookup by anything
// but the identifier — so a refresh cannot find the row its predecessor made.
// The rows an exchange leaves behind are dead within the idle timeout and the
// sweeper removes them; SignOut ends the current one and revokes the family
// so no exchange mints another.
//
// The row is created outside signin's transaction, which is the ordering the
// sessions package documents rather than an oversight: its store may not be in
// a SQL database at all and takes no Tx. A sign-in whose hooks then refuse
// leaves a session nobody holds a token for, which idles out.
package session

import (
	"context"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/platform-go/v14/sessions"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// ClaimSessionID is the token claim naming the session row.
const ClaimSessionID = "fijo_session"

// Data is what a session row holds beyond what the store keeps for itself.
type Data struct {
	_ struct{} `json:"-"`

	// UserID is the subject the token was minted for, checked against the
	// token's subject on every request so a row cannot vouch for a stranger.
	UserID string `json:"userID"`

	// FamilyID is the refresh-token family the token belongs to, checked
	// against the token's sid claim for the same reason.
	FamilyID string `json:"familyID"`
}

// ErrNilStore is a claims builder built over nothing.
var ErrNilStore = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil session store")

// Holder is the sessions holder a user's rows are filed under.
func Holder(principal *identity.Principal) sessions.Holder {
	return sessions.Holder{Principal: principal.User.ID, Scope: principal.User.Scope}
}

// ClaimsBuilder returns the signin.ClaimsBuilder that creates the session row
// for every token the sign-in service mints and adds its identifier to
// signin's default claims.
func ClaimsBuilder(store sessions.Store[Data]) (signin.ClaimsBuilder, error) {
	if store == nil {
		return nil, ErrNilStore
	}

	return func(ctx context.Context, input *signin.ClaimsInput) (map[string]any, error) {
		claims, err := signin.DefaultClaims(ctx, input)
		if err != nil {
			return nil, err
		}

		created, err := store.NewFor(ctx, Holder(input.Principal), sessions.Metadata{}, &Data{
			UserID:   input.Principal.User.ID,
			FamilyID: input.FamilyID,
		})
		if err != nil {
			return nil, platformerrors.Wrap(err, "establishing the session behind a token")
		}

		claims[ClaimSessionID] = created.ID

		return claims, nil
	}, nil
}
