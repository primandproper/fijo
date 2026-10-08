package resetmail

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/fijo/internal/config"

	"github.com/primandproper/platform-go/v15/authentication/passwordreset"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/email"
	emailmock "github.com/primandproper/primitives-go/v2/email/mock"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestMailer(t *testing.T) {
	t.Parallel()

	links := config.PasswordResetConfig{LinkBaseURL: "https://studio.example/reset-password"}
	from := config.MailConfig{FromAddress: "no-reply@fijo.example", FromName: "Fijo"}

	t.Run("sends the link to the user's address", func(t *testing.T) {
		t.Parallel()

		var sent *email.OutboundEmailMessage

		emailer := &emailmock.EmailerMock{
			SendEmailFunc: func(_ context.Context, details *email.OutboundEmailMessage) error {
				sent = details

				return nil
			},
		}

		m, err := New(emailer, links, from, nil)
		must.NoError(t, err)

		now := time.Now()

		must.NoError(t, m.SendPasswordReset(t.Context(), &passwordreset.Mail{
			User: &identity.User{ID: "u", Username: "maria", EmailAddress: "maria@example.com", DisplayName: "Maria <b>"},
			Issuance: &passwordreset.Issuance{
				Secret: "s3cret",
				Token:  &passwordreset.Token{CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
			},
		}))

		must.NotNil(t, sent)
		test.Eq(t, "maria@example.com", sent.ToAddress)
		test.Eq(t, "no-reply@fijo.example", sent.FromAddress)
		test.StrContains(t, sent.HTMLContent, "https://studio.example/reset-password?token=s3cret")
		test.StrContains(t, sent.HTMLContent, "Maria &lt;b&gt;")
		test.StrContains(t, sent.HTMLContent, "1h0m0s")
	})

	t.Run("refuses to be built without a link base or a sender", func(t *testing.T) {
		t.Parallel()

		emailer := &emailmock.EmailerMock{}

		_, err := New(emailer, config.PasswordResetConfig{}, from, nil)
		test.ErrorIs(t, err, ErrNoLinkBase)

		_, err = New(emailer, links, config.MailConfig{}, nil)
		test.ErrorIs(t, err, ErrNoFromAddress)

		_, err = New(nil, links, from, nil)
		test.ErrorIs(t, err, ErrNilEmailer)
	})

	t.Run("refuses mail that names nobody", func(t *testing.T) {
		t.Parallel()

		m, err := New(&emailmock.EmailerMock{}, links, from, nil)
		must.NoError(t, err)

		test.ErrorIs(t, m.SendPasswordReset(t.Context(), nil), ErrNilMail)
		test.ErrorIs(t, m.SendPasswordReset(t.Context(), &passwordreset.Mail{}), ErrMailNamesNobody)
	})
}
