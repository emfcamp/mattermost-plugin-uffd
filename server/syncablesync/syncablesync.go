// Package syncablesyncengine syncs groups in Mattermost downwards into "syncables" (teams/channels) that use them.
//
// This is used as a workaround because Mattermost doesn't support plugin-created groups
// being synced by the platform into teams/channels, even though it supports creating the
// association.
package syncablesync

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

type ServiceAPI interface {
	SortSyncableTargets([]SyncableTarget)
	SyncableHandlers() map[SyncableType]SyncableHandler
	FetchGroupsAndSyncables(context.Context) ([]Group, error)
	UnremovableUserIDs(context.Context) ([]string, error)
}

type Engine struct {
	API ServiceAPI
}

type SyncableType string

type SyncableTarget struct {
	Type SyncableType
	ID   string
}

func (t SyncableTarget) String() string {
	return fmt.Sprintf("%s/%s", t.Type, t.ID)
}

type Syncable struct {
	Target SyncableTarget

	GrantsAdmin bool
}

type RosterMember struct {
	UserID  string
	IsAdmin bool

	ServiceType any
}

type SyncableHandler interface {
	FetchRoster(context.Context, SyncableTarget) ([]RosterMember, error)
	AddMembers(context.Context, SyncableTarget, []RosterMember) error
	DeleteMembers(context.Context, SyncableTarget, []RosterMember) error
	UpdateMembers(context.Context, SyncableTarget, []RosterMember) error
	IsAddOnlyTarget(context.Context, SyncableTarget) (bool, error)
}

type Group struct {
	ID   string
	Name string

	Members   []string
	Syncables []Syncable

	ServiceType any
}

func rosterMap(rms []RosterMember) map[string]RosterMember {
	out := make(map[string]RosterMember, len(rms))
	for _, rm := range rms {
		out[rm.UserID] = rm
	}
	return out
}

// FullSync performs a sync of all syncables.
// The algorithm is summarized as:
//  1. Fetch list of groups with syncables.
//  2. Group list by syncables.
//  3. For each syncable:
//     a. Fetch the existing roster
//     b. Compute the desired roster
//     c. Generate operations to match (add/remove/promote/demote)
//     c. Apply operations
func (e *Engine) FullSync(ctx context.Context) error {
	l := ctxlog.FromContext(ctx)

	groups, err := e.API.FetchGroupsAndSyncables(ctx)
	if err != nil {
		return fmt.Errorf("fetching list of groups to sync: %w", err)
	}
	l.WithField("groups", groups).Debug("found groups")

	syncablesToGroups := map[SyncableTarget][]Group{}
	for _, group := range groups {
		for _, syncable := range group.Syncables {
			// Groups should not appear multiple times - it doesn't make sense for them to grant both non-admin and admin.
			// TODO(lukegb): validate this?
			syncablesToGroups[syncable.Target] = append(syncablesToGroups[syncable.Target], group)
		}
	}

	// Users which should not be removed from channels that they 'shouldn't be in'.
	unremovableUsersList, err := e.API.UnremovableUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("fetching list of 'unremovable' user IDs: %w", err)
	}
	unremovableUsers := stringset.FromSlice(unremovableUsersList)

	sortedSyncableTarget := slices.Collect(maps.Keys(syncablesToGroups))
	e.API.SortSyncableTargets(sortedSyncableTarget)

	handlers := e.API.SyncableHandlers()
	for _, syncableTarget := range sortedSyncableTarget {
		l := l.WithField("syncableTarget", syncableTarget)
		groups := syncablesToGroups[syncableTarget]
		handler := handlers[syncableTarget.Type]
		gotRoster, err := handler.FetchRoster(ctx, syncableTarget)
		if err != nil {
			return fmt.Errorf("fetching roster for %#v: %w", syncableTarget, err)
		}
		gotRosterMap := rosterMap(gotRoster)
		l.Debugf("fetched roster (%d entries)", len(gotRoster))

		wantRosterMap := make(map[string]RosterMember)
		for _, g := range groups {
			var s Syncable
			for _, gs := range g.Syncables {
				if gs.Target == syncableTarget {
					s = gs
				}
			}
			for _, m := range g.Members {
				rm := wantRosterMap[m]
				rm.UserID = m
				rm.ServiceType = gotRosterMap[m].ServiceType
				// A user may be a member of multiple groups.
				// If any of them grant admin, give admin.
				rm.IsAdmin = rm.IsAdmin || s.GrantsAdmin
				wantRosterMap[m] = rm
			}
		}

		var membersToAdd, membersToDelete, membersToUpdate []RosterMember
		for uid, wantRM := range wantRosterMap {
			gotRM, ok := gotRosterMap[uid]
			if !ok {
				membersToAdd = append(membersToAdd, wantRM)
			} else if gotRM.IsAdmin != wantRM.IsAdmin {
				membersToUpdate = append(membersToUpdate, wantRM)
			}
		}
		addOnly, err := handler.IsAddOnlyTarget(ctx, syncableTarget)
		if err != nil {
			return fmt.Errorf("checking if %#v is add-only: %w", syncableTarget, err)
		}
		if !addOnly {
			for uid, gotRM := range gotRosterMap {
				if _, ok := wantRosterMap[uid]; !ok {
					if unremovableUsers.Contains(uid) {
						// Don't touch unremovable users at all.
						continue
					}
					membersToDelete = append(membersToDelete, gotRM)
				}
			}
		} else {
			// We do want to ensure people aren't admins unless they should be.
			for uid, gotRM := range gotRosterMap {
				if !gotRM.IsAdmin {
					// If they're not already an admin then it doesn't matter.
					continue
				}
				if unremovableUsers.Contains(uid) {
					// Don't touch unremovable users at all.
					continue
				}
				if _, ok := wantRosterMap[uid]; !ok {
					gotRM.IsAdmin = false
					membersToUpdate = append(membersToUpdate, gotRM)
				}
			}
		}
		if len(membersToAdd)+len(membersToDelete)+len(membersToUpdate) > 0 {
			l.Infof("computed diff (%d adds, %d deletes, %d updates)", len(membersToAdd), len(membersToDelete), len(membersToUpdate))
		} else {
			l.Debugf("no-op diff completed")
		}

		var mergedErr error
		if len(membersToAdd) > 0 {
			if err := handler.AddMembers(ctx, syncableTarget, membersToAdd); err != nil {
				mergedErr = errors.Join(mergedErr, err)
			}
		}
		if len(membersToUpdate) > 0 {
			if err := handler.UpdateMembers(ctx, syncableTarget, membersToUpdate); err != nil {
				mergedErr = errors.Join(mergedErr, err)
			}
		}
		if len(membersToDelete) > 0 {
			if err := handler.DeleteMembers(ctx, syncableTarget, membersToDelete); err != nil {
				mergedErr = errors.Join(mergedErr, err)
			}
		}
		if mergedErr != nil {
			return fmt.Errorf("performing updates for %#v: %w", syncableTarget, mergedErr)
		}
		if len(membersToAdd)+len(membersToDelete)+len(membersToUpdate) > 0 {
			l.Infof("applied diff (%d adds, %d deletes, %d updates)", len(membersToAdd), len(membersToDelete), len(membersToUpdate))
		} else {
			l.Debugf("no-op diff completed")
		}
	}
	return nil
}
