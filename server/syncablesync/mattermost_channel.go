package syncablesync

import (
	"context"
	"fmt"
	"strings"

	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/mattermost/mattermost/server/public/model"
)

type mattermostChannelHandler struct {
	api mattermostChannelAPI
}

type mattermostChannelAPI interface {
	GetChannelMembers(channelID string, page, perPage int) (model.ChannelMembers, *model.AppError)
	AddUserToChannel(channelID, userID, onBehalfOfUser string) (*model.ChannelMember, *model.AppError)
	DeleteChannelMember(channelID, userID string) *model.AppError
	UpdateChannelMemberRoles(channelID, userID, roles string) (*model.ChannelMember, *model.AppError)
}

func (h *mattermostChannelHandler) FetchRoster(ctx context.Context, st SyncableTarget) ([]RosterMember, error) {
	if st.Type != mattermostSyncableTypeChannel {
		return nil, fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}

	channelMembers, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]model.ChannelMember, error) {
		ms, appErr := h.api.GetChannelMembers(st.ID, page, perPage)
		if appErr != nil {
			return nil, appErr
		}
		return ms, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching channel members for channel %v: %w", st.ID, err)
	}

	out := make([]RosterMember, len(channelMembers))
	for n, cm := range channelMembers {
		out[n] = RosterMember{
			UserID:  cm.UserId,
			IsAdmin: cm.SchemeAdmin,

			ServiceType: &cm,
		}
	}
	return out, nil
}

func (h *mattermostChannelHandler) AddMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	// TODO(lukegb): if a member is supposed to be a member of a channel, but isn't a member of the enclosing team, what do we do?
	// Maybe we should add them to the team automatically?
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	for _, rm := range rms {
		_, appErr := h.api.AddUserToChannel(st.ID, rm.UserID, "")
		if appErr != nil {
			return fmt.Errorf("adding %s to channel %s: %w", rm.UserID, st.ID, appErr)
		}
		if rm.IsAdmin {
			_, appErr := h.api.UpdateChannelMemberRoles(st.ID, rm.UserID, "channel_user channel_admin")
			if appErr != nil {
				return fmt.Errorf("updating channel member %s in channel %s to admin: %w", rm.UserID, st.ID, appErr)
			}
		}
	}
	return nil
}

func (h *mattermostChannelHandler) DeleteMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	for _, rm := range rms {
		appErr := h.api.DeleteChannelMember(st.ID, rm.UserID)
		if appErr != nil {
			return fmt.Errorf("removing %s from channel %s: %w", rm.UserID, st.ID, appErr)
		}
	}
	return nil
}

func (h *mattermostChannelHandler) UpdateMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	for _, rm := range rms {
		oldTM := rm.ServiceType.(*model.ChannelMember)
		roles := stringset.FromSlice(oldTM.GetRoles())
		isAdmin := roles.Contains("channel_admin")

		if rm.IsAdmin != isAdmin {
			if rm.IsAdmin {
				roles.Add("channel_admin")
			} else {
				roles.Remove("channel_admin")
			}

			_, appErr := h.api.UpdateChannelMemberRoles(st.ID, rm.UserID, strings.Join(roles.Sorted(), " "))
			if appErr != nil {
				return fmt.Errorf("updating roles for %s in channel %s to %s: %w", rm.UserID, st.ID, roles.Sorted(), appErr)
			}
		}
	}
	return nil
}
