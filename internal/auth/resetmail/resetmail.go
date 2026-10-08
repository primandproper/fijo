// Package resetmail sends the password-reset link.
//
// platform-go's passwordreset service issues the token and spends it; what it
// leaves to the consumer is the mail, because the link's host, the sender and
// the wording are the product's. This is Fijo's: one message, in English for
// now — owners are English-speaking in the first tenant, and the bilingual
// obligation the design carries is for client-facing strings — carrying a link
// to the frontend page that completes the reset.
package resetmail

import (
	"context"
	"fmt"
	"html"

	"github.com/primandproper/fijo/internal/config"

	"github.com/primandproper/platform-go/v15/authentication/passwordreset"
	"github.com/primandproper/primitives-go/v2/email"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
)

const serviceName = "reset_mailer"

// The refusals.
var (
	ErrNilEmailer      = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil emailer")
	ErrNoLinkBase      = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "the password reset link base URL is required to send reset mail")
	ErrNoFromAddress   = platformerrors.Wrap(platformerrors.ErrEmptyInputParameter, "the mail from-address is required to send reset mail")
	ErrNilMail         = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil reset mail")
	ErrMailNamesNobody = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "reset mail names no user or no issuance")
)

// Mailer implements passwordreset.Mailer over an email.Emailer.
type Mailer struct {
	emailer email.Emailer
	o11y    observability.Observer
	from    config.MailConfig
	links   config.PasswordResetConfig
}

var _ passwordreset.Mailer = (*Mailer)(nil)

// New builds a Mailer. It refuses at construction what it could not do at send
// time: a link base or a sender left blank.
func New(emailer email.Emailer, links config.PasswordResetConfig, from config.MailConfig, pillars *observability.Pillars) (*Mailer, error) {
	switch {
	case emailer == nil:
		return nil, ErrNilEmailer
	case links.LinkBaseURL == "":
		return nil, ErrNoLinkBase
	case from.FromAddress == "":
		return nil, ErrNoFromAddress
	}

	m := &Mailer{emailer: emailer, links: links, from: from}

	if pillars != nil {
		m.o11y = observability.NewObserver(serviceName, pillars.Logger, pillars.TracerProvider)
	} else {
		m.o11y = observability.NewObserver(serviceName, nil, nil)
	}

	return m, nil
}

// SendPasswordReset mails the link.
func (m *Mailer) SendPasswordReset(ctx context.Context, mail *passwordreset.Mail) (err error) {
	ctx, op := m.o11y.Begin(ctx)
	defer op.End()

	if mail == nil {
		return op.Error(ErrNilMail, "sending reset mail")
	}

	if mail.User == nil || mail.Issuance == nil {
		return op.Error(ErrMailNamesNobody, "sending reset mail")
	}

	op.Set("identity.user_id", mail.User.ID)

	link, err := m.links.ResetLink(mail.Issuance.Secret)
	if err != nil {
		return op.Error(err, "building the reset link")
	}

	name := mail.User.DisplayName
	if name == "" {
		name = mail.User.Username
	}

	if err = m.emailer.SendEmail(ctx, &email.OutboundEmailMessage{
		ToAddress:   mail.User.EmailAddress,
		ToName:      name,
		FromAddress: m.from.FromAddress,
		FromName:    m.from.FromName,
		Subject:     "Reset your Fijo password",
		HTMLContent: Body(name, link, mail.Issuance.Token.ExpiresAt.Sub(mail.Issuance.Token.CreatedAt).Round(60e9).String()),
	}); err != nil {
		return op.Error(err, "sending reset mail")
	}

	return nil
}

// Body renders the message. It is exported so the wording has a test.
func Body(name, link, lifetime string) string {
	return fmt.Sprintf(
		"<p>Hi %s,</p>"+
			"<p>Somebody asked to reset the password on your Fijo account. If that was you, follow this link within %s:</p>"+
			"<p><a href=%q>%s</a></p>"+
			"<p>If it was not you, you can ignore this message; the link only works once and expires on its own.</p>",
		html.EscapeString(name), html.EscapeString(lifetime), link, html.EscapeString(link),
	)
}
