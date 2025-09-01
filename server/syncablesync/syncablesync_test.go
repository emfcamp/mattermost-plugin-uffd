package syncablesync

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

const dummySyncableType = SyncableType("flarp")

type dummyService struct {
	groups          []Group
	syncableMembers map[SyncableTarget][]RosterMember
}

var _ ServiceAPI = ((*dummyService)(nil))

func (s *dummyService) SortSyncableTargets(st []SyncableTarget) {}

func (s *dummyService) SyncableHandlers() map[SyncableType]SyncableHandler {
	return map[SyncableType]SyncableHandler{
		dummySyncableType: &dummyServiceHandler{s},
	}
}

func (s *dummyService) FetchGroupsAndSyncables(context.Context) ([]Group, error) {
	return s.groups, nil
}

type dummyServiceHandler struct {
	s *dummyService
}

var _ SyncableHandler = ((*dummyServiceHandler)(nil))

func (d *dummyServiceHandler) IsAddOnlyTarget(context.Context, SyncableTarget) (bool, error) {
	return false, nil
}
func (d *dummyServiceHandler) FetchRoster(ctx context.Context, st SyncableTarget) ([]RosterMember, error) {
	return d.s.syncableMembers[st], nil
}

func (d *dummyServiceHandler) AddMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if d.s.syncableMembers == nil {
		d.s.syncableMembers = make(map[SyncableTarget][]RosterMember)
	}

	d.s.syncableMembers[st] = append(d.s.syncableMembers[st], rms...)
	return nil
}

func (d *dummyServiceHandler) DeleteMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	removeUserIDs := stringset.New()
	for _, rm := range rms {
		removeUserIDs.Add(rm.UserID)
	}

	var newMembers []RosterMember
	for _, rm := range d.s.syncableMembers[st] {
		if !removeUserIDs.Contains(rm.UserID) {
			newMembers = append(newMembers, rm)
		}
	}
	d.s.syncableMembers[st] = newMembers

	return nil
}

func (d *dummyServiceHandler) UpdateMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	updateByUserID := rosterMap(rms)

	var newMembers []RosterMember
	for _, rm := range d.s.syncableMembers[st] {
		upRM, ok := updateByUserID[rm.UserID]
		if ok {
			rm.IsAdmin = upRM.IsAdmin
		}
		newMembers = append(newMembers, rm)
	}
	d.s.syncableMembers[st] = newMembers

	return nil
}

func TestFullSync(t *testing.T) {
	t.Parallel()

	dummyST := SyncableTarget{
		Type: dummySyncableType,
		ID:   "dummy",
	}
	_ = dummyST

	tcs := []struct {
		name                string
		s                   *dummyService
		wantSyncableMembers map[SyncableTarget][]RosterMember
	}{{
		name: "nothing to do",
		s:    &dummyService{},
	}, {
		name: "create new member",
		s: &dummyService{
			groups: []Group{{
				ID:      "group",
				Name:    "group name",
				Members: []string{"user1"},
				Syncables: []Syncable{{
					Target: dummyST,
				}},
			}},
		},
		wantSyncableMembers: map[SyncableTarget][]RosterMember{
			dummyST: {{
				UserID:  "user1",
				IsAdmin: false,
			}},
		},
	}, {
		name: "create new member as admin",
		s: &dummyService{
			groups: []Group{{
				ID:      "group",
				Name:    "group name",
				Members: []string{"user1"},
				Syncables: []Syncable{{
					Target:      dummyST,
					GrantsAdmin: true,
				}},
			}},
		},
		wantSyncableMembers: map[SyncableTarget][]RosterMember{
			dummyST: {{
				UserID:  "user1",
				IsAdmin: true,
			}},
		},
	}, {
		name: "removes members if no longer present",
		s: &dummyService{
			groups: []Group{{
				ID:   "group",
				Name: "group name",
				Syncables: []Syncable{{
					Target:      dummyST,
					GrantsAdmin: true,
				}},
			}},
			syncableMembers: map[SyncableTarget][]RosterMember{
				dummyST: {{
					UserID:  "user1",
					IsAdmin: true,
				}, {
					UserID: "user2",
				}},
			},
		},
		wantSyncableMembers: map[SyncableTarget][]RosterMember{
			dummyST: {},
		},
	}, {
		name: "promotes members if needed",
		s: &dummyService{
			groups: []Group{{
				ID:      "group",
				Name:    "group name",
				Members: []string{"user1", "user2"},
				Syncables: []Syncable{{
					Target:      dummyST,
					GrantsAdmin: true,
				}},
			}},
			syncableMembers: map[SyncableTarget][]RosterMember{
				dummyST: {{
					UserID:  "user1",
					IsAdmin: true,
				}, {
					UserID: "user2",
				}},
			},
		},
		wantSyncableMembers: map[SyncableTarget][]RosterMember{
			dummyST: {{
				UserID:  "user1",
				IsAdmin: true,
			}, {
				UserID:  "user2",
				IsAdmin: true,
			}},
		},
	}, {
		name: "demotes members if needed",
		s: &dummyService{
			groups: []Group{{
				ID:      "group",
				Name:    "group name",
				Members: []string{"user1", "user2"},
				Syncables: []Syncable{{
					Target: dummyST,
				}},
			}},
			syncableMembers: map[SyncableTarget][]RosterMember{
				dummyST: {{
					UserID:  "user1",
					IsAdmin: true,
				}, {
					UserID: "user2",
				}},
			},
		},
		wantSyncableMembers: map[SyncableTarget][]RosterMember{
			dummyST: {{
				UserID: "user1",
			}, {
				UserID: "user2",
			}},
		},
	}}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			e := &Engine{
				API: tc.s,
			}
			if err := e.FullSync(ctx); err != nil {
				t.Fatalf("FullSync: %v", err)
			}

			if diff := cmp.Diff(tc.s.syncableMembers, tc.wantSyncableMembers, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("FullSync diff syncableMembers (-got +want):\n%s", diff)
			}
		})
	}
}
