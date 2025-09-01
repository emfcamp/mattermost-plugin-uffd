package syncengine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type TestService struct {
	Users  map[string]*User[string]
	Groups []*Group[string]
	Teams  []Team
}

// SaveGroups implements ServiceAPI.
func (t *TestService) SaveGroups(ctx context.Context, gs []*Group[string]) error {
	t.Groups = gs
	return nil
}

// SaveTeams implements ServiceAPI.
func (t *TestService) SaveTeams(ctx context.Context, ts []Team) error {
	t.Teams = ts
	return nil
}

// CreateUsers implements ServiceAPI.
func (t *TestService) CreateUsers(ctx context.Context, newUsers []*User[int]) ([]*User[string], error) {
	var out []*User[string]
	for _, u := range newUsers {
		newUser := &User[string]{
			UserID:      fmt.Sprintf("user:::%s", u.Username),
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Email:       u.Email,
			Active:      u.Active,

			IDPUserID: u.UserID,
		}
		newUser.ServiceUserID = newUser.UserID
		t.Users[newUser.UserID] = newUser

		outUser := newUser.ShallowClone()
		outUser.ServiceUser = "populated on create"
		out = append(out, outUser)
	}
	return out, nil
}

// FetchUsers implements ServiceAPI.
func (t *TestService) FetchUsers(context.Context) ([]*User[string], error) {
	var out []*User[string]
	for _, u := range t.Users {
		out = append(out, u.ShallowClone())
	}
	return out, nil
}

// UpdateUsers implements ServiceAPI.
func (t *TestService) UpdateUsers(ctx context.Context, inUsers []*User[string]) ([]*User[string], []bool, error) {
	var out []*User[string]
	var updated []bool
	for _, inUser := range inUsers {
		u, ok := t.Users[inUser.UserID]
		if !ok {
			return nil, nil, fmt.Errorf("user %v not found", inUser.UserID)
		}

		didUpdate := false
		if u.Username != inUser.Username {
			u.Username = inUser.Username
			didUpdate = true
		}
		if u.DisplayName != inUser.DisplayName {
			u.DisplayName = inUser.DisplayName
			didUpdate = true
		}
		if u.Email != inUser.Email {
			u.Email = inUser.Email
			didUpdate = true
		}
		if u.Active != inUser.Active {
			u.Active = inUser.Active
			didUpdate = true
		}

		outUser := u.ShallowClone()
		outUser.ServiceUser = "populated on update"
		out = append(out, outUser)
		updated = append(updated, didUpdate)
	}
	return out, updated, nil
}

var _ ServiceAPI = (*TestService)(nil)

type TestIDP struct {
	Users  []*User[int]
	Groups []*Group[int]
}

// FetchGroups implements IdPAPI.
func (t *TestIDP) FetchGroups(context.Context) ([]*Group[int], error) {
	var out []*Group[int]
	for _, g := range t.Groups {
		out = append(out, g.ShallowClone())
	}
	return out, nil
}

// FetchUserByID implements IdPAPI.
func (t *TestIDP) FetchUserByID(ctx context.Context, uid int) (*User[int], error) {
	for _, u := range t.Users {
		if u.UserID == uid {
			return u.ShallowClone(), nil
		}
	}
	return nil, fmt.Errorf("not found")
}

// FetchUsers implements IdPAPI.
func (t *TestIDP) FetchUsers(context.Context) ([]*User[int], error) {
	var out []*User[int]
	for _, u := range t.Users {
		out = append(out, u.ShallowClone())
	}
	return out, nil
}

var _ IDPAPI = (*TestIDP)(nil)

type clonable[T any] interface {
	ShallowClone() T
}

func mutate[T clonable[T]](x T, f func(T)) T {
	out := x.ShallowClone()
	f(out)
	return out
}

func mutateTeam(x Team, f func(*Team)) Team {
	f(&x)
	return x
}

func benchmarkFullSync(b *testing.B, users, groups int) {
	idp := &TestIDP{
		Users:  make([]*User[int], users),
		Groups: make([]*Group[int], groups),
	}
	uids := make([]int, users)
	for n := range idp.Users {
		idp.Users[n] = &User[int]{
			UserID:      1000 + n,
			Username:    fmt.Sprintf("testuser%d", n),
			Email:       fmt.Sprintf("user%d@example.com", n),
			DisplayName: fmt.Sprintf("Test User %d", n),
			Active:      true,
		}
		uids[n] = 1000 + n
	}
	for n := range idp.Groups {
		idp.Groups[n] = &Group[int]{
			GroupID:       1000 + n,
			Name:          fmt.Sprintf("testgrp%d", n),
			MemberUserIDs: uids,
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	for n := 0; n < b.N; n++ {
		s := &TestService{
			Users: make(map[string]*User[string]),
		}
		syncEngine := &SyncEngine{
			IDP:     idp,
			Service: s,
		}
		if _, err := syncEngine.FullSync(ctx); err != nil {
			b.Fatalf("FullSync: %v", err)
		}
	}
}

func BenchmarkFullSync1000Users500Groups(b *testing.B) { benchmarkFullSync(b, 1000, 500) }

func TestFullSync(t *testing.T) {
	t.Parallel()
	emptyService := func() *TestService {
		return &TestService{
			Users: make(map[string]*User[string]),
		}
	}
	var idpLukegb = &User[int]{
		UserID:      1000,
		Username:    "lukegb",
		Email:       "lukegb@example.com",
		DisplayName: "Luke GB",
		Active:      true,
	}
	var serviceLukegb = &User[string]{
		UserID:        "user:::lukegb",
		Username:      "lukegb",
		Email:         "lukegb@example.com",
		DisplayName:   "Luke GB",
		Active:        true,
		IDPUserID:     1000,
		ServiceUserID: "user:::lukegb",
	}

	var idpFoo = &Group[int]{
		GroupID: 1000,
		Name:    "foo",
	}
	var serviceFoo = &Group[string]{
		GroupID: "foo",
		Name:    "foo",
	}

	var idpTeamFooLeads = &Group[int]{
		GroupID: 2000,
		Name:    "moderation_foo",
	}
	var idpTeamFooMembers = &Group[int]{
		GroupID: 2001,
		Name:    "team_foo",
	}
	var serviceTeamFoo = Team{
		Name: "foo",
	}

	tcs := []struct {
		name             string
		idp              *TestIDP
		service          *TestService
		additionalGroups []string

		wantUsers   map[string]*User[string]
		wantGroups  []*Group[string]
		wantTeams   []Team
		wantOutcome *Outcome
	}{{
		name: "create user",
		idp: &TestIDP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
		},
		service: emptyService(),
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantOutcome: &Outcome{
			CreatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.ServiceUser = "populated on create"
				}),
			},
		},
	}, {
		name: "disable user that disappears from IdP",
		idp: &TestIDP{
			Users:  []*User[int]{},
			Groups: []*Group[int]{idpFoo.ShallowClone()},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": mutate(serviceLukegb, func(u *User[string]) {
				u.Active = false
			}),
		},
		wantOutcome: &Outcome{
			UpdatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.Active = false
					u.ServiceUser = "populated on update"
				}),
			},
		},
	}, {
		name: "additional groups sync",
		idp: &TestIDP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{
				mutate(idpFoo, func(g *Group[int]) {
					g.MemberUserIDs = append(g.MemberUserIDs, idpLukegb.UserID)
				}),
			},
		},
		additionalGroups: []string{idpFoo.Name},
		service:          emptyService(),
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: []*Group[string]{
			mutate(serviceFoo, func(g *Group[string]) {
				g.MemberUserIDs = append(g.MemberUserIDs, "user:::lukegb")
			}),
		},
		wantOutcome: &Outcome{
			CreatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.ServiceUser = "populated on create"
				}),
			},
		},
	}, {
		name: "team mapping",
		idp: &TestIDP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{
				mutate(idpTeamFooLeads, func(g *Group[int]) {
					g.MemberUserIDs = append(g.MemberUserIDs, idpLukegb.UserID)
				}),
				mutate(idpTeamFooMembers, func(g *Group[int]) {
					g.MemberUserIDs = append(g.MemberUserIDs, idpLukegb.UserID)
				}),
			},
		},
		service: emptyService(),
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantTeams: []Team{
			mutateTeam(serviceTeamFoo, func(t *Team) {
				t.Leads = []string{"user:::lukegb"}
				t.Members = []string{"user:::lukegb"}
			}),
		},
		wantOutcome: &Outcome{
			CreatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.ServiceUser = "populated on create"
				}),
			},
		},
	}}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncEngine := &SyncEngine{
				IDP:              tc.idp,
				Service:          tc.service,
				GroupStore:       tc.service,
				AdditionalGroups: tc.additionalGroups,
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			out, err := syncEngine.FullSync(ctx)
			if err != nil {
				t.Fatalf("FullSync: %v", err)
			}

			if diff := cmp.Diff(tc.service.Users, tc.wantUsers, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("users diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(tc.service.Groups, tc.wantGroups, cmpopts.SortSlices(func(g1, g2 *Group[string]) int { return strings.Compare(g1.Name, g2.Name) })); diff != "" {
				t.Errorf("groups diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(tc.service.Teams, tc.wantTeams, cmpopts.SortSlices(func(t1, t2 Team) int { return strings.Compare(t1.Name, t2.Name) })); diff != "" {
				t.Errorf("teams diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(out, tc.wantOutcome, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("outcome diff (-got +want):\n%s", diff)
			}

			// run the sync again, and ensure it did nothing this time...

			out2, err := syncEngine.FullSync(ctx)
			if err != nil {
				t.Fatalf("FullSync (again): %v", err)
			}

			if diff := cmp.Diff(tc.service.Users, tc.wantUsers, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("users (again) diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(tc.service.Groups, tc.wantGroups, cmpopts.SortSlices(func(g1, g2 *Group[string]) int { return strings.Compare(g1.Name, g2.Name) })); diff != "" {
				t.Errorf("groups (again) diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(tc.service.Teams, tc.wantTeams, cmpopts.SortSlices(func(t1, t2 Team) int { return strings.Compare(t1.Name, t2.Name) })); diff != "" {
				t.Errorf("teams (again) diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(out2, &Outcome{}, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("outcome (again) diff (-got +want):\n%s", diff)
			}
		})
	}
}
