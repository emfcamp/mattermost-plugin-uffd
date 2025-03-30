package syncengine

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

type TestService struct {
	Users  map[string]*User[string]
	Groups map[string]*Group[string]
}

// CreateGroups implements ServiceAPI.
func (t *TestService) CreateGroups(ctx context.Context, newGroups []*Group[int]) ([]*Group[string], error) {
	var out []*Group[string]
	for _, g := range newGroups {
		newGroup := &Group[string]{
			GroupID: fmt.Sprintf("group:::%d", g.GroupID),
			Name:    g.Name,
		}
		t.Groups[newGroup.GroupID.(string)] = newGroup

		out = append(out, newGroup.ShallowClone())
	}
	return out, nil
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

			IdPUserID: u.UserID,
		}
		newUser.ServiceUserID = newUser.UserID
		t.Users[newUser.UserID] = newUser

		outUser := newUser.ShallowClone()
		outUser.ServiceUser = "populated on create"
		out = append(out, outUser)
	}
	return out, nil
}

// DeleteGroups implements ServiceAPI.
func (t *TestService) DeleteGroups(ctx context.Context, groups []*Group[string]) error {
	for _, g := range groups {
		delete(t.Groups, g.GroupID.(string))
	}
	return nil
}

// FetchGroups implements ServiceAPI.
func (t *TestService) FetchGroups(context.Context) ([]*Group[string], error) {
	var out []*Group[string]
	for _, g := range t.Groups {
		out = append(out, g.ShallowClone())
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

// AddGroupMembers implements ServiceAPI.
func (t *TestService) AddGroupMembers(ctx context.Context, groupID string, newMembers []string) error {
	g, ok := t.Groups[groupID]
	if !ok {
		return fmt.Errorf("group not found")
	}

	members := stringset.FromSlice(g.MemberUserIDs)
	members.Add(newMembers...)
	g.MemberUserIDs = members.Sorted()
	return nil
}

// RemoveGroupMembers implements ServiceAPI.
func (t *TestService) RemoveGroupMembers(ctx context.Context, groupID string, removeMembers []string) error {
	g, ok := t.Groups[groupID]
	if !ok {
		return fmt.Errorf("group not found")
	}

	members := stringset.FromSlice(g.MemberUserIDs)
	members.Remove(removeMembers...)
	g.MemberUserIDs = members.Sorted()
	return nil
}

// UpdateGroups implements ServiceAPI.
func (t *TestService) UpdateGroups(ctx context.Context, groups []*Group[string]) ([]*Group[string], []bool, error) {
	var out []*Group[string]
	var updated []bool
	for _, inGroup := range groups {
		g, ok := t.Groups[inGroup.GroupID.(string)]
		if !ok {
			return nil, nil, fmt.Errorf("group %v not found", inGroup.GroupID)
		}

		didUpdate := g.Name != inGroup.Name
		g.Name = inGroup.Name
		out = append(out, g.ShallowClone())
		updated = append(updated, didUpdate)
	}
	return out, updated, nil
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

type TestIdP struct {
	Users  []*User[int]
	Groups []*Group[int]
}

// FetchGroups implements IdPAPI.
func (t *TestIdP) FetchGroups(context.Context) ([]*Group[int], error) {
	var out []*Group[int]
	for _, g := range t.Groups {
		out = append(out, g.ShallowClone())
	}
	return out, nil
}

// FetchUserByID implements IdPAPI.
func (t *TestIdP) FetchUserByID(ctx context.Context, uid int) (*User[int], error) {
	for _, u := range t.Users {
		if u.UserID == uid {
			return u.ShallowClone(), nil
		}
	}
	return nil, fmt.Errorf("not found")
}

// FetchUsers implements IdPAPI.
func (t *TestIdP) FetchUsers(context.Context) ([]*User[int], error) {
	var out []*User[int]
	for _, u := range t.Users {
		out = append(out, u.ShallowClone())
	}
	return out, nil
}

var _ IdPAPI = (*TestIdP)(nil)

type clonable[T any] interface {
	ShallowClone() T
}

func mutate[T clonable[T]](x T, f func(T)) T {
	out := x.ShallowClone()
	f(out)
	return out
}

func benchmarkFullSync(b *testing.B, users, groups int) {
	idp := &TestIdP{
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
			Users:  make(map[string]*User[string]),
			Groups: make(map[string]*Group[string]),
		}
		syncEngine := &SyncEngine{
			IdP:     idp,
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
			Users:  make(map[string]*User[string]),
			Groups: make(map[string]*Group[string]),
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
		IdPUserID:     1000,
		ServiceUserID: "user:::lukegb",
	}

	var idpFoo = &Group[int]{
		GroupID: 1000,
		Name:    "foo",
	}
	var serviceFoo = &Group[string]{
		GroupID: "group:::1000",
		Name:    "foo",
	}

	tcs := []struct {
		name    string
		idp     *TestIdP
		service *TestService

		wantUsers   map[string]*User[string]
		wantGroups  map[string]*Group[string]
		wantOutcome *Outcome
	}{{
		name: "create user",
		idp: &TestIdP{
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
		name: "create group",
		idp: &TestIdP{
			Groups: []*Group[int]{idpFoo.ShallowClone()},
		},
		service: emptyService(),
		wantGroups: map[string]*Group[string]{
			"group:::1000": serviceFoo.ShallowClone(),
		},
		wantOutcome: &Outcome{
			CreatedGroups: []*Group[string]{
				serviceFoo.ShallowClone(),
			},
		},
	}, {
		name: "create user + group",
		idp: &TestIdP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{mutate(idpFoo, func(g *Group[int]) {
				g.MemberUserIDs = []int{1000}
			})},
		},
		service: emptyService(),
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: map[string]*Group[string]{
			"group:::1000": mutate(serviceFoo, func(g *Group[string]) {
				g.MemberUserIDs = []string{"user:::lukegb"}
			}),
		},
		wantOutcome: &Outcome{
			CreatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.ServiceUser = "populated on create"
				}),
			},
			CreatedGroups: []*Group[string]{
				mutate(serviceFoo.ShallowClone(), func(g *Group[string]) {
					g.MemberUserIDs = []string{"user:::lukegb"}
				}),
			},
			CreatedMemberships: []Membership{{
				GroupID: "group:::1000",
				UserID:  "user:::lukegb",
			}},
		},
	}, {
		name: "do nothing with existing user + group",
		idp: &TestIdP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{mutate(idpFoo, func(g *Group[int]) {
				g.MemberUserIDs = []int{1000}
			})},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
			Groups: map[string]*Group[string]{
				"group:::1000": mutate(serviceFoo, func(g *Group[string]) {
					g.MemberUserIDs = []string{"user:::lukegb"}
				}),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: map[string]*Group[string]{
			"group:::1000": mutate(serviceFoo, func(g *Group[string]) {
				g.MemberUserIDs = []string{"user:::lukegb"}
			}),
		},
		wantOutcome: &Outcome{},
	}, {
		name: "add existing user to existing group",
		idp: &TestIdP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{mutate(idpFoo, func(g *Group[int]) {
				g.MemberUserIDs = []int{1000}
			})},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
			Groups: map[string]*Group[string]{
				"group:::1000": serviceFoo.ShallowClone(),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: map[string]*Group[string]{
			"group:::1000": mutate(serviceFoo, func(g *Group[string]) {
				g.MemberUserIDs = []string{"user:::lukegb"}
			}),
		},
		wantOutcome: &Outcome{
			UpdatedGroups: []*Group[string]{
				mutate(serviceFoo, func(g *Group[string]) {
					g.MemberUserIDs = []string{"user:::lukegb"}
				}),
			},
			CreatedMemberships: []Membership{{
				GroupID: "group:::1000",
				UserID:  "user:::lukegb",
			}},
		},
	}, {
		name: "remove user from group",
		idp: &TestIdP{
			Users:  []*User[int]{idpLukegb.ShallowClone()},
			Groups: []*Group[int]{idpFoo.ShallowClone()},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
			Groups: map[string]*Group[string]{
				"group:::1000": mutate(serviceFoo, func(g *Group[string]) {
					g.MemberUserIDs = []string{"user:::lukegb"}
				}),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: map[string]*Group[string]{
			"group:::1000": serviceFoo.ShallowClone(),
		},
		wantOutcome: &Outcome{
			UpdatedGroups: []*Group[string]{
				serviceFoo.ShallowClone(),
			},
			RemovedMemberships: []Membership{{
				GroupID: "group:::1000",
				UserID:  "user:::lukegb",
			}},
		},
	}, {
		name: "disable user that disappears from IdP",
		idp: &TestIdP{
			Users:  []*User[int]{},
			Groups: []*Group[int]{idpFoo.ShallowClone()},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
			Groups: map[string]*Group[string]{
				"group:::1000": serviceFoo.ShallowClone(),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": mutate(serviceLukegb, func(u *User[string]) {
				u.Active = false
			}),
		},
		wantGroups: map[string]*Group[string]{
			"group:::1000": serviceFoo.ShallowClone(),
		},
		wantOutcome: &Outcome{
			UpdatedGroups: []*Group[string]{},
			UpdatedUsers: []*User[string]{
				mutate(serviceLukegb, func(u *User[string]) {
					u.Active = false
					u.ServiceUser = "populated on update"
				}),
			},
		},
	}, {
		name: "delete group that disappears from IdP",
		idp: &TestIdP{
			Users: []*User[int]{idpLukegb.ShallowClone()},
		},
		service: &TestService{
			Users: map[string]*User[string]{
				"user:::lukegb": serviceLukegb.ShallowClone(),
			},
			Groups: map[string]*Group[string]{
				"group:::1000": serviceFoo.ShallowClone(),
			},
		},
		wantUsers: map[string]*User[string]{
			"user:::lukegb": serviceLukegb.ShallowClone(),
		},
		wantGroups: map[string]*Group[string]{},
		wantOutcome: &Outcome{
			DeletedGroups: []*Group[string]{
				serviceFoo.ShallowClone(),
			},
		},
	}}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncEngine := &SyncEngine{
				IdP:     tc.idp,
				Service: tc.service,
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
			if diff := cmp.Diff(tc.service.Groups, tc.wantGroups, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("groups diff (-got +want):\n%s", diff)
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
			if diff := cmp.Diff(tc.service.Groups, tc.wantGroups, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("groups (again) diff (-got +want):\n%s", diff)
			}
			if diff := cmp.Diff(out2, &Outcome{}, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("outcome (again) diff (-got +want):\n%s", diff)
			}
		})
	}
}
