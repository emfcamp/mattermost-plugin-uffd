package syncablesync

import (
	"context"
	"fmt"
	"strings"

	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/mattermost/mattermost/server/public/model"
)

type mattermostTeamHandler struct {
	api mattermostTeamAPI
}

type mattermostTeamAPI interface {
	GetTeamMembers(teamID string, page, perPage int) ([]*model.TeamMember, *model.AppError)
	CreateTeamMembers(teamID string, userID []string, onBehalfOfUser string) ([]*model.TeamMember, *model.AppError)
	DeleteTeamMember(teamID, userID, onBehalfOfUser string) *model.AppError
	UpdateTeamMemberRoles(teamID, userID, roles string) (*model.TeamMember, *model.AppError)
}

func (h *mattermostTeamHandler) FetchRoster(ctx context.Context, st SyncableTarget) ([]RosterMember, error) {
	if st.Type != mattermostSyncableTypeTeam {
		return nil, fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeTeam)
	}
	teamMembers, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]*model.TeamMember, error) {
		ms, appErr := h.api.GetTeamMembers(st.ID, page, perPage)
		if appErr != nil {
			return nil, appErr
		}
		return ms, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching team members for team %v: %w", st.ID, err)
	}

	out := make([]RosterMember, len(teamMembers))
	for n, tm := range teamMembers {
		out[n] = RosterMember{
			UserID:  tm.UserId,
			IsAdmin: tm.SchemeAdmin,

			ServiceType: tm,
		}
	}
	return out, nil
}

func (h *mattermostTeamHandler) AddMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeTeam {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeTeam)
	}
	userIDs := make([]string, len(rms))
	adminIDs := stringset.New()
	for n, rm := range rms {
		userIDs[n] = rm.UserID
		if rm.IsAdmin {
			adminIDs.Add(rm.UserID)
		}
	}

	_, appErr := h.api.CreateTeamMembers(st.ID, userIDs, "")
	if appErr != nil {
		return fmt.Errorf("creating %d team members in team %v: %w", len(userIDs), st.ID, appErr)
	}
	for _, adminID := range adminIDs.Sorted() {
		_, appErr := h.api.UpdateTeamMemberRoles(st.ID, adminID, "team_user team_admin")
		if appErr != nil {
			return fmt.Errorf("updating team member %s in team %s to admin: %w", adminID, st.ID, appErr)
		}
	}
	return nil
}

func (h *mattermostTeamHandler) DeleteMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeTeam {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeTeam)
	}
	for _, rm := range rms {
		appErr := h.api.DeleteTeamMember(st.ID, rm.UserID, "")
		if appErr != nil {
			return fmt.Errorf("removing %s from team %s: %w", rm.UserID, st.ID, appErr)
		}
	}
	return nil
}

func (h *mattermostTeamHandler) UpdateMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeTeam {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeTeam)
	}
	for _, rm := range rms {
		oldTM := rm.ServiceType.(*model.TeamMember)
		roles := stringset.FromSlice(oldTM.GetRoles())
		isAdmin := roles.Contains("team_admin")

		if rm.IsAdmin != isAdmin {
			if rm.IsAdmin {
				roles.Add("team_admin")
			} else {
				roles.Remove("team_admin")
			}

			_, appErr := h.api.UpdateTeamMemberRoles(st.ID, rm.UserID, strings.Join(roles.Sorted(), " "))
			if appErr != nil {
				return fmt.Errorf("updating roles for %s in team %s to %s: %w", rm.UserID, st.ID, roles.Sorted(), appErr)
			}
		}
	}
	return nil
}
