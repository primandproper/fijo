// Package auditing records who did what to the directory and at the door.
//
// platform-go's identity and signin services each expose a Hooks interface
// that runs inside the transaction of the operation it follows, which is what
// makes an audit entry and the row it describes one fact: a registration whose
// entry cannot be written is a registration that did not happen. This package
// is Fijo's implementation of both, over the platform's own audit.Recorder.
//
// Every entry is scoped to the studio it concerns. The identity rows themselves
// live in the global directory, but what an owner reads back is "what happened
// in my studio", and a studio is Fijo's tenant. An operation with no studio to
// name — a sign-in by an operator with no membership — lands in the global
// scope, which is the operator's.
//
// The actor is the user the operation was about. The hooks do not see the
// request, so for the operator writes — a status change, an archival — the
// actor is recorded as the subject with the metadata saying so; the calling
// operator is on the gRPC access log by method and session. Phase 1's domain
// commands, which run inside handlers that hold the caller, record the caller.
package auditing

import (
	"context"
	"strings"
	"time"

	"github.com/primandproper/platform-go/v14/audit"
	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/identifiers"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The resource types entries name. They are the identity nouns and the
// sign-in door; the domains add their own.
const (
	ResourceUser       = "identity.user"
	ResourceAccount    = "identity.account"
	ResourceMembership = "identity.membership"
	ResourceInvitation = "identity.invitation"
	ResourceSignIn     = "signin"
)

// The metadata keys entries carry.
const (
	KeyUserID      = "user_id"
	KeyAccountID   = "account_id"
	KeyFields      = "fields"
	KeyRoles       = "roles"
	KeyStatus      = "status"
	KeyReason      = "reason"
	KeyHandle      = "handle"
	KeyAdmin       = "administrative"
	KeyFamilyID    = "family_id"
	KeyNewOwner    = "new_owner_user_id"
	KeyRemovedUser = "removed_user_id"
)

// ErrNilRecorder is a Hooks built over nothing.
var ErrNilRecorder = platformerrors.Wrap(platformerrors.ErrNilInputParameter, "nil audit recorder")

// The two noop implementations are embedded under distinct names so that the
// methods this package does not record fall through to a no-op and a hook the
// platform adds later is a compile-time no-op rather than a missing method.
type (
	identityNoop = identity.NoopHooks
	signinNoop   = signin.NoopHooks
)

// Hooks implements identity.Hooks and signin.Hooks over one recorder.
type Hooks struct {
	identityNoop
	signinNoop

	recorder audit.Recorder
}

var (
	_ identity.Hooks = (*Hooks)(nil)
	_ signin.Hooks   = (*Hooks)(nil)
)

// New builds the hooks.
func New(recorder audit.Recorder) (*Hooks, error) {
	if recorder == nil {
		return nil, ErrNilRecorder
	}

	return &Hooks{recorder: recorder}, nil
}

// StudioScope is the tenancy an entry about accountID is filed under.
func StudioScope(accountID string) tenancy.Scope {
	if accountID == "" {
		return tenancy.Global()
	}

	return tenancy.Of(accountID)
}

func (h *Hooks) record(ctx context.Context, tx database.Tx, accountID string, entries ...*audit.Entry) error {
	return h.recorder.Record(ctx, tx, StudioScope(accountID), entries...)
}

func entry(resourceType, resourceID string, event audit.EventType, actorUserID string, metadata map[string]string) *audit.Entry {
	if metadata == nil {
		metadata = map[string]string{}
	}

	if actorUserID == "" {
		actorUserID = audit.ActorUnattributed
	}

	return &audit.Entry{
		ID:           identifiers.New(),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		EventType:    event,
		Actor:        audit.Actor{ID: actorUserID, Type: audit.ActorUser},
		Metadata:     metadata,
	}
}

func joined(values []string) string { return strings.Join(values, ",") }

// AfterRegister records the user, the studio and the owner membership a
// registration minted.
func (h *Hooks) AfterRegister(ctx context.Context, tx database.Tx, _ tenancy.Scope, r *identity.Registration) error {
	if r == nil || r.User == nil || r.Account == nil {
		return nil
	}

	entries := []*audit.Entry{
		entry(ResourceUser, r.User.ID, audit.EventCreated, r.User.ID, map[string]string{KeyReason: "registered"}),
		entry(ResourceAccount, r.Account.ID, audit.EventCreated, r.User.ID, map[string]string{KeyUserID: r.User.ID}),
	}

	if r.Membership != nil {
		entries = append(entries, entry(ResourceMembership, r.Membership.ID, audit.EventCreated, r.User.ID, map[string]string{
			KeyUserID:    r.User.ID,
			KeyAccountID: r.Account.ID,
			KeyRoles:     joined(r.Membership.Roles),
		}))
	}

	return h.record(ctx, tx, r.Account.ID, entries...)
}

// AfterRegisterWithInvitation records a user joining a studio on an invitation.
func (h *Hooks) AfterRegisterWithInvitation(ctx context.Context, tx database.Tx, _ tenancy.Scope, r *identity.InvitedRegistration) error {
	if r == nil || r.User == nil || r.Membership == nil {
		return nil
	}

	return h.record(ctx, tx, r.Membership.BelongsToAccount,
		entry(ResourceUser, r.User.ID, audit.EventCreated, r.User.ID, map[string]string{KeyReason: "registered_with_invitation"}),
		entry(ResourceMembership, r.Membership.ID, audit.EventCreated, r.User.ID, map[string]string{
			KeyUserID:    r.User.ID,
			KeyAccountID: r.Membership.BelongsToAccount,
			KeyRoles:     joined(r.Membership.Roles),
		}),
	)
}

// AfterInvite records an invitation sent.
func (h *Hooks) AfterInvite(ctx context.Context, tx database.Tx, _ tenancy.Scope, inv *identity.Invitation) error {
	if inv == nil {
		return nil
	}

	return h.record(ctx, tx, inv.BelongsToAccount,
		entry(ResourceInvitation, inv.ID, audit.EventCreated, inv.FromUser, map[string]string{
			KeyAccountID: inv.BelongsToAccount,
			KeyRoles:     joined(inv.Roles),
		}),
	)
}

// AfterAcceptInvitation records an invitation answered yes.
func (h *Hooks) AfterAcceptInvitation(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *identity.Acceptance) error {
	if a == nil || a.Invitation == nil || a.Membership == nil {
		return nil
	}

	return h.record(ctx, tx, a.Membership.BelongsToAccount,
		entry(ResourceInvitation, a.Invitation.ID, audit.EventUpdated, a.Membership.BelongsToUser, map[string]string{KeyStatus: "accepted"}),
		entry(ResourceMembership, a.Membership.ID, audit.EventCreated, a.Membership.BelongsToUser, map[string]string{
			KeyUserID:    a.Membership.BelongsToUser,
			KeyAccountID: a.Membership.BelongsToAccount,
			KeyRoles:     joined(a.Membership.Roles),
		}),
	)
}

// AfterRejectInvitation records an invitation answered no.
func (h *Hooks) AfterRejectInvitation(ctx context.Context, tx database.Tx, _ tenancy.Scope, inv *identity.Invitation) error {
	if inv == nil {
		return nil
	}

	return h.record(ctx, tx, inv.BelongsToAccount,
		entry(ResourceInvitation, inv.ID, audit.EventUpdated, "", map[string]string{KeyStatus: "rejected"}),
	)
}

// AfterCancelInvitation records an invitation withdrawn by the studio.
func (h *Hooks) AfterCancelInvitation(ctx context.Context, tx database.Tx, _ tenancy.Scope, inv *identity.Invitation) error {
	if inv == nil {
		return nil
	}

	return h.record(ctx, tx, inv.BelongsToAccount,
		entry(ResourceInvitation, inv.ID, audit.EventUpdated, inv.FromUser, map[string]string{KeyStatus: "cancelled"}),
	)
}

// AfterCreateAccount records a second studio created by an existing user.
func (h *Hooks) AfterCreateAccount(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *identity.Account, m *identity.Membership) error {
	if a == nil {
		return nil
	}

	entries := []*audit.Entry{
		entry(ResourceAccount, a.ID, audit.EventCreated, a.OwnerUserID, map[string]string{KeyUserID: a.OwnerUserID}),
	}

	if m != nil {
		entries = append(entries, entry(ResourceMembership, m.ID, audit.EventCreated, a.OwnerUserID, map[string]string{
			KeyUserID:    m.BelongsToUser,
			KeyAccountID: a.ID,
			KeyRoles:     joined(m.Roles),
		}))
	}

	return h.record(ctx, tx, a.ID, entries...)
}

// AfterTransferAccountOwnership records a studio changing hands.
func (h *Hooks) AfterTransferAccountOwnership(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *identity.Account, previousOwner string) error {
	if a == nil {
		return nil
	}

	return h.record(ctx, tx, a.ID,
		entry(ResourceAccount, a.ID, audit.EventUpdated, previousOwner, map[string]string{
			KeyFields:   "owner_user_id",
			KeyNewOwner: a.OwnerUserID,
		}),
	)
}

// AfterArchiveUser records a user archived, with every membership that went
// with them.
func (h *Hooks) AfterArchiveUser(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User, memberships []*identity.Membership) error {
	if u == nil {
		return nil
	}

	if err := h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventArchived, u.ID, nil)); err != nil {
		return err
	}

	for _, m := range memberships {
		if m == nil {
			continue
		}

		if err := h.record(ctx, tx, m.BelongsToAccount, entry(ResourceMembership, m.ID, audit.EventArchived, u.ID, map[string]string{
			KeyUserID:    m.BelongsToUser,
			KeyAccountID: m.BelongsToAccount,
			KeyReason:    "user_archived",
		})); err != nil {
			return err
		}
	}

	return nil
}

// AfterArchiveAccount records a studio archived.
func (h *Hooks) AfterArchiveAccount(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *identity.Account, memberships []*identity.Membership) error {
	if a == nil {
		return nil
	}

	entries := []*audit.Entry{entry(ResourceAccount, a.ID, audit.EventArchived, a.OwnerUserID, nil)}

	for _, m := range memberships {
		if m == nil {
			continue
		}

		entries = append(entries, entry(ResourceMembership, m.ID, audit.EventArchived, a.OwnerUserID, map[string]string{
			KeyUserID:    m.BelongsToUser,
			KeyAccountID: a.ID,
			KeyReason:    "account_archived",
		}))
	}

	return h.record(ctx, tx, a.ID, entries...)
}

// AfterUpdateUserAccountStatus records a suspension or its lifting.
func (h *Hooks) AfterUpdateUserAccountStatus(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User, previous identity.AccountStatus) error {
	if u == nil {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{
		KeyFields:         "account_status",
		KeyStatus:         u.AccountStatus.String(),
		"previous_status": previous.String(),
	}))
}

// AfterSetUserServiceRoles records an operator role granted or withdrawn.
func (h *Hooks) AfterSetUserServiceRoles(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User, previous []string) error {
	if u == nil {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{
		KeyFields:        "service_roles",
		KeyRoles:         joined(u.ServiceRoles),
		"previous_roles": joined(previous),
	}))
}

// AfterUpdateProfile records a profile edit by the fields it touched.
func (h *Hooks) AfterUpdateProfile(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User, fields []string) error {
	if u == nil || len(fields) == 0 {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{KeyFields: joined(fields)}))
}

// AfterUpdateAccount records a studio edit by the fields it touched. The
// studio's time zone is one of them.
func (h *Hooks) AfterUpdateAccount(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *identity.Account, fields []string) error {
	if a == nil || len(fields) == 0 {
		return nil
	}

	return h.record(ctx, tx, a.ID, entry(ResourceAccount, a.ID, audit.EventUpdated, a.OwnerUserID, map[string]string{KeyFields: joined(fields)}))
}

// AfterSetMembershipRoles records a member's roles changing.
func (h *Hooks) AfterSetMembershipRoles(ctx context.Context, tx database.Tx, _ tenancy.Scope, m *identity.Membership, previous []string) error {
	if m == nil {
		return nil
	}

	return h.record(ctx, tx, m.BelongsToAccount, entry(ResourceMembership, m.ID, audit.EventUpdated, m.BelongsToUser, map[string]string{
		KeyUserID:        m.BelongsToUser,
		KeyAccountID:     m.BelongsToAccount,
		KeyFields:        "roles",
		KeyRoles:         joined(m.Roles),
		"previous_roles": joined(previous),
	}))
}

// AfterRemoveMembership records a member leaving or being removed.
func (h *Hooks) AfterRemoveMembership(ctx context.Context, tx database.Tx, _ tenancy.Scope, m *identity.Membership, removedUserID string) error {
	if m == nil {
		return nil
	}

	return h.record(ctx, tx, m.BelongsToAccount, entry(ResourceMembership, m.ID, audit.EventArchived, removedUserID, map[string]string{
		KeyUserID:      m.BelongsToUser,
		KeyAccountID:   m.BelongsToAccount,
		KeyRemovedUser: removedUserID,
	}))
}

// AfterUpdateUserPassword records a password change without the password.
func (h *Hooks) AfterUpdateUserPassword(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User, requiresChange bool) error {
	if u == nil {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{
		KeyFields:                  "hashed_password",
		"requires_password_change": boolString(requiresChange),
	}))
}

// AfterAuthenticate records a proven sign-in, before any token is minted.
func (h *Hooks) AfterAuthenticate(ctx context.Context, tx database.Tx, _ tenancy.Scope, a *signin.Authentication) error {
	if a == nil || a.Principal == nil || a.Principal.User == nil {
		return nil
	}

	return h.record(ctx, tx, a.Principal.ActiveAccountID, entry(ResourceSignIn, a.Principal.User.ID, audit.EventAccessed, a.Principal.User.ID, map[string]string{
		KeyAccountID: a.Principal.ActiveAccountID,
		KeyAdmin:     boolString(a.Administrative),
	}))
}

// AfterIssueToken records the token family a sign-in produced, so a family
// revoked later can be traced to the sign-in that opened it.
func (h *Hooks) AfterIssueToken(ctx context.Context, tx database.Tx, _ tenancy.Scope, s *signin.SignIn) error {
	if s == nil || s.Principal == nil || s.Principal.User == nil {
		return nil
	}

	return h.record(ctx, tx, s.Principal.ActiveAccountID, entry(ResourceSignIn, s.Principal.User.ID, audit.EventCreated, s.Principal.User.ID, map[string]string{
		KeyAccountID: s.Principal.ActiveAccountID,
		KeyFamilyID:  s.FamilyID,
		KeyAdmin:     boolString(s.Administrative),
		"expires_at": s.ExpiresAt.UTC().Format(time.RFC3339),
	}))
}

// AfterFailedSignIn records a refused attempt against the handle it named.
// Failed attempts have no studio, so they are the operator's to read.
func (h *Hooks) AfterFailedSignIn(ctx context.Context, tx database.Tx, _ tenancy.Scope, f *signin.FailedSignIn) error {
	if f == nil {
		return nil
	}

	reason := ""
	if f.Reason != nil {
		reason = f.Reason.Error()
	}

	return h.record(ctx, tx, "", entry(ResourceSignIn, f.UserID, audit.EventOther, f.UserID, map[string]string{
		KeyHandle: f.Handle,
		KeyReason: reason,
		KeyAdmin:  boolString(f.Administrative),
	}))
}

// AfterUpdatePassword records a signed-in password change.
func (h *Hooks) AfterUpdatePassword(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User) error {
	if u == nil {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{KeyFields: "hashed_password", KeyReason: "update_password"}))
}

// AfterAttachPassword records a passwordless user acquiring a password.
func (h *Hooks) AfterAttachPassword(ctx context.Context, tx database.Tx, _ tenancy.Scope, u *identity.User) error {
	if u == nil {
		return nil
	}

	return h.record(ctx, tx, "", entry(ResourceUser, u.ID, audit.EventUpdated, u.ID, map[string]string{KeyFields: "hashed_password", KeyReason: "attach_password"}))
}

func boolString(b bool) string {
	if b {
		return "true"
	}

	return "false"
}
