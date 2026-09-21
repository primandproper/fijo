package session

import (
	"context"
	"testing"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/platform-go/v14/sessions"
	sessionsmock "github.com/primandproper/platform-go/v14/sessions/mock"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func principal() *identity.Principal {
	return &identity.Principal{
		User:            &identity.User{ID: "user-1", Scope: tenancy.Global()},
		ActiveAccountID: "studio-1",
	}
}

func TestClaimsBuilder(t *testing.T) {
	t.Parallel()

	t.Run("creates a session per mint and names it in the claims", func(t *testing.T) {
		t.Parallel()

		store := &sessionsmock.StoreMock[Data]{
			NewForFunc: func(_ context.Context, holder sessions.Holder, _ sessions.Metadata, data *Data) (*sessions.Session[Data], error) {
				test.Eq(t, "user-1", holder.Principal)
				test.Eq(t, "user-1", data.UserID)
				test.Eq(t, "family-1", data.FamilyID)

				return &sessions.Session[Data]{ID: "session-1", Data: data}, nil
			},
		}

		build, err := ClaimsBuilder(store)
		must.NoError(t, err)

		claims, err := build(t.Context(), &signin.ClaimsInput{Principal: principal(), FamilyID: "family-1"})
		must.NoError(t, err)

		test.Eq(t, "session-1", claims[ClaimSessionID])
		test.Eq(t, "family-1", claims[signin.ClaimFamilyID])
		test.Eq(t, "studio-1", claims[signin.ClaimAccountID])
		test.SliceLen(t, 1, store.NewForCalls())
	})

	t.Run("a store that refuses refuses the mint", func(t *testing.T) {
		t.Parallel()

		store := &sessionsmock.StoreMock[Data]{
			NewForFunc: func(context.Context, sessions.Holder, sessions.Metadata, *Data) (*sessions.Session[Data], error) {
				return nil, sessions.ErrNilBackend
			},
		}

		build, err := ClaimsBuilder(store)
		must.NoError(t, err)

		_, err = build(t.Context(), &signin.ClaimsInput{Principal: principal(), FamilyID: "family-1"})
		must.ErrorIs(t, err, sessions.ErrNilBackend)
	})

	t.Run("refuses a nil store", func(t *testing.T) {
		t.Parallel()

		_, err := ClaimsBuilder(nil)
		must.ErrorIs(t, err, ErrNilStore)
	})
}
