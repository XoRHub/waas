package service

import (
	"context"
	"fmt"

	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/operator/pkg/naming"
)

// placementCollision names an existing account whose personal namespace
// a candidate username would resolve to as well.
type placementCollision struct {
	Username  string
	Namespace string
}

// placementUsernameConflict returns the collision a candidate username
// would create, or nil when it creates none.
//
// It compares naming.PersonalNamespace, NOT naming.Sanitize: the two
// diverge above the token budget, where the resolution truncates and
// appends a hash of the raw value. Two 59-character usernames can share
// a sanitized form and still land in different namespaces — comparing
// the sanitized form alone would refuse an account that collides with
// nothing.
//
// A username with no DNS-usable character (Cyrillic, CJK, Greek, Arabic)
// resolves through the account id instead (naming.IdentitySegment), and
// two ids never collide — so PersonalNamespace returns "" here, with no
// id to offer, and the candidate is correctly reported as conflict-free.
//
// Why the platform checks this at all: the built-in placement default
// gives each user their own namespace, derived from naming.Sanitize of
// their username — a LOSSY projection ("alice.smith", "alice_smith" and
// "Alice Smith" all become "alice-smith"). The database's UNIQUE
// constraint is on the raw column, so such accounts coexist and then
// share one namespace, one ownership label and one ResourceQuota: the
// isolation the per-user default exists for is silently lost.
//
// Why it refuses instead of disambiguating: an IdP already numbers its
// homonyms (jdoe, jdoe2) and rewriting that convention is not the
// platform's job. So the anomaly is refused at the two doors — account
// creation, where an admin fixes it on the spot, and first SSO login,
// where only the audit trail can, the directory being out of reach.
// Planned escape hatch for that second case: a configurable alias claim
// on the SSO token, consulted as the placement name when the username
// collides, so a directory that cannot be changed still has a way in.
//
// An EXACT duplicate is not reported here: the UNIQUE constraint already
// rejects it, with a message that says so plainly.
func placementUsernameConflict(ctx context.Context, users repository.UserRepository, candidate string) (*placementCollision, error) {
	personal := naming.PersonalNamespace(candidate, "")
	if personal == "" {
		return nil, nil
	}
	existing, err := users.ListUsernames(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking username placement collision: %w", err)
	}
	for _, username := range existing {
		if username != candidate && naming.PersonalNamespace(username, "") == personal {
			return &placementCollision{Username: username, Namespace: personal}, nil
		}
	}
	return nil, nil
}
