package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/datastore"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
)

const (
	addTeamDialogName      = "addTeam"
	addUserDialogName      = "addUser"
	removeMemberDialogName = "removeTeam"
)

func (p *Plugin) registerDialogHandlers(apiRouter *mux.Router) {
	// /plugin/%s/api/v1/dialog/...
	apiRouter.Path("/" + addTeamDialogName + "/{channel}").Methods(http.MethodPost).HandlerFunc(p.addTeamResponseHandler)
	apiRouter.Path("/" + addUserDialogName + "/{channel}").Methods(http.MethodPost).HandlerFunc(p.addUserResponseHandler)
	apiRouter.Path("/" + removeMemberDialogName + "/{channel}").Methods(http.MethodPost).HandlerFunc(p.removeMemberResponseHandler)
}

func (p *Plugin) makeDialogRequest(triggerID string, dialogName string, dialog model.Dialog) (model.OpenDialogRequest, error) {
	manifest, err := p.client.System.GetManifest()
	if err != nil {
		return model.OpenDialogRequest{}, fmt.Errorf("GetManifest: %w", err)
	}

	return model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       fmt.Sprintf("/plugins/%s/api/v1/dialog/%s", manifest.Id, dialogName),
		Dialog:    dialog,
	}, nil
}

func (p *Plugin) openAddTeamDialog(ctx context.Context, triggerID string, channel *model.Channel, channelInfo *datastore.ChannelInfo) error {
	var teamOptions []*model.PostActionOptions
	teams, err := p.datastore().LoadTeams(ctx)
	if err != nil {
		return fmt.Errorf("loading teams from datastore: %w", err)
	}
	slices.SortFunc(teams, func(t1, t2 syncengine.Team) int {
		return strings.Compare(t1.Name, t2.Name)
	})
	for _, team := range teams {
		var extraInfos []string
		for _, members := range channelInfo.Members {
			if members.Type == datastore.ACLElementTypeTeamLead && members.Value == team.Name {
				// Team Leads are Members of this channel
				extraInfos = append(extraInfos, "team leads are already channel admins")
			}
			if members.Type == datastore.ACLElementTypeTeamMember && members.Value == team.Name {
				// Team Members are Members of this channel
				extraInfos = append(extraInfos, "team members are already channel admins")
			}
		}
		for _, admins := range channelInfo.Admins {
			if admins.Type == datastore.ACLElementTypeTeamLead && admins.Value == team.Name {
				// Team Leads are admins of this channel
				extraInfos = append(extraInfos, "team leads are already channel members")
			}
			if admins.Type == datastore.ACLElementTypeTeamMember && admins.Value == team.Name {
				// Team Members are admins of this channel
				extraInfos = append(extraInfos, "team members are already channel members")
			}
		}
		var extraInfo string
		if len(extraInfos) > 0 {
			extraInfo = fmt.Sprintf(" (%s)", strings.Join(extraInfos, ", "))
		}
		teamOptions = append(teamOptions, &model.PostActionOptions{
			Text:  team.Name + extraInfo,
			Value: team.Name,
		})
	}

	d := model.Dialog{
		Title: fmt.Sprintf("Add a team to %s", channel.Name),
		Elements: []model.DialogElement{{
			DisplayName: "Team",
			Name:        "team",
			Type:        "select",
			Options:     teamOptions,
		}, {
			Name:        "addLeadsOnly",
			DisplayName: "Only add team leads",
			Placeholder: "Only team leads from the selected team will be added.",
			Optional:    true,
			Type:        "bool",
		}, {
			Name:        "grantAdmin",
			DisplayName: "Grant channel admin",
			Placeholder: "Team members/team leads (depending on the checkbox above) of the selected team will be granted channel admin.",
			Optional:    true,
			Type:        "bool",
		}},
	}
	dialogRequest, err := p.makeDialogRequest(triggerID, addTeamDialogName+"/"+channel.Id, d)
	if err != nil {
		return err
	}
	if err := p.client.Frontend.OpenInteractiveDialog(dialogRequest); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) addTeamResponseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json; encoding=utf-8")
	channelID := mux.Vars(r)["channel"]
	ctx := r.Context()
	userID := r.Header.Get("Mattermost-User-ID")
	l := ctxlog.FromContext(ctx).WithField("user", userID).WithField("channel", channelID)

	_, chInfo, err := p.fetchChannelAndCheckPermission(userID, channelID)
	if err != nil {
		l.WithError(err).Errorf("fetching channel information to add a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: err.Error(),
		})
		return
	}

	var payload model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		l.WithError(err).Errorf("parsing dialog payload to add a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Invalid dialog payload: " + err.Error(),
		})
		return
	}

	// OK, check if this is going to match any existing ACL entry (and if so, remove it)
	grantAdmin := payload.Submission["grantAdmin"].(bool)
	leadsOnly := payload.Submission["addLeadsOnly"].(bool)
	aclEntryType := datastore.ACLElementTypeTeamMember
	if leadsOnly {
		aclEntryType = datastore.ACLElementTypeTeamLead
	}
	team := payload.Submission["team"].(string)

	chInfo.Admins = slices.DeleteFunc(chInfo.Admins, func(e datastore.ACLElement) bool {
		return e.Type == aclEntryType && e.Value == team
	})
	chInfo.Members = slices.DeleteFunc(chInfo.Members, func(e datastore.ACLElement) bool {
		return e.Type == aclEntryType && e.Value == team
	})
	entry := datastore.ACLElement{
		Type:  aclEntryType,
		Value: team,
	}
	if grantAdmin {
		chInfo.Admins = append(chInfo.Admins, entry)
	} else {
		chInfo.Members = append(chInfo.Members, entry)
	}
	if err := p.datastore().SaveChannel(ctx, chInfo); err != nil {
		l.WithError(err).Errorf("saving while add a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Saving new channel ACL: " + err.Error(),
		})
		return
	}

	if err := p.runSync(ctx, "team-acl-add"); err != nil {
		l.WithError(err).Errorf("sync while adding a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Syncing new channel ACL: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{})
}

func (p *Plugin) getNameForUser(userID string) string {
	u, err := p.client.User.Get(userID)
	if err != nil {
		return userID
	}
	if u.GetFullName() == u.Username {
		return u.Username
	}
	return fmt.Sprintf("%s (%s)", u.GetFullName(), u.Username)
}

func (p *Plugin) openRemoveMemberDialog(ctx context.Context, triggerID string, channel *model.Channel, channelInfo *datastore.ChannelInfo) error {
	var teamOptions []*model.PostActionOptions
	for _, members := range channelInfo.Members {
		var friendlyName string
		switch members.Type {
		case datastore.ACLElementTypeTeamLead:
			friendlyName = fmt.Sprintf("%s leads", members.Value)
		case datastore.ACLElementTypeTeamMember:
			friendlyName = fmt.Sprintf("%s members", members.Value)
		case datastore.ACLElementTypeUser:
			friendlyName = fmt.Sprintf("User %s", p.getNameForUser(members.Value))
		}
		teamOptions = append(teamOptions, &model.PostActionOptions{
			Text:  friendlyName,
			Value: fmt.Sprintf("member/%s/%s", members.Value, members.Type),
		})
	}
	for _, admins := range channelInfo.Admins {
		var friendlyName string
		switch admins.Type {
		case datastore.ACLElementTypeTeamLead:
			friendlyName = fmt.Sprintf("%s leads (channel admin)", admins.Value)
		case datastore.ACLElementTypeTeamMember:
			friendlyName = fmt.Sprintf("%s members (channel admin)", admins.Value)
		case datastore.ACLElementTypeUser:
			friendlyName = fmt.Sprintf("User %s (channel admin)", p.getNameForUser(admins.Value))
		}
		teamOptions = append(teamOptions, &model.PostActionOptions{
			Text:  friendlyName,
			Value: fmt.Sprintf("admin/%s/%s", admins.Value, admins.Type),
		})
	}

	var introText string
	if channel.IsOpen() {
		introText = "Because this channel is public, removing a channel member will not kick them from the channel, but only stops new team members being automatically added to the channel, or being granted 'channel admin' status."
	}

	d := model.Dialog{
		Title:            fmt.Sprintf("Remove a channel member from %s", channel.Name),
		IntroductionText: introText,
		Elements: []model.DialogElement{{
			DisplayName: "Member",
			Name:        "member",
			Type:        "select",
			Options:     teamOptions,
		}},
	}
	dialogRequest, err := p.makeDialogRequest(triggerID, removeMemberDialogName+"/"+channel.Id, d)
	if err != nil {
		return err
	}
	if err := p.client.Frontend.OpenInteractiveDialog(dialogRequest); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) removeMemberResponseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json; encoding=utf-8")
	channelID := mux.Vars(r)["channel"]
	ctx := r.Context()
	userID := r.Header.Get("Mattermost-User-ID")
	l := ctxlog.FromContext(ctx).WithField("user", userID).WithField("channel", channelID)

	_, chInfo, err := p.fetchChannelAndCheckPermission(userID, channelID)
	if err != nil {
		l.WithError(err).Errorf("fetching channel information to remove a member from a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: err.Error(),
		})
		return
	}

	var payload model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		l.WithError(err).Errorf("parsing dialog payload to remove a member from a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Invalid dialog payload: " + err.Error(),
		})
		return
	}

	member := payload.Submission["member"].(string)

	toStr := func(aclType string, e datastore.ACLElement) string {
		return fmt.Sprintf("%s/%s/%s", aclType, e.Value, e.Type)
	}
	chInfo.Admins = slices.DeleteFunc(chInfo.Admins, func(e datastore.ACLElement) bool {
		return toStr("admin", e) == member
	})
	chInfo.Members = slices.DeleteFunc(chInfo.Members, func(e datastore.ACLElement) bool {
		return toStr("member", e) == member
	})
	if err := p.datastore().SaveChannel(ctx, chInfo); err != nil {
		l.WithError(err).Errorf("saving while removing a member from a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Saving new channel ACL: " + err.Error(),
		})
		return
	}

	if err := p.runSync(ctx, "channel-acl-remove"); err != nil {
		l.WithError(err).Errorf("sync while removing a member from a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Syncing new channel ACL: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{})
}

func (p *Plugin) openAddUserDialog(ctx context.Context, triggerID string, channel *model.Channel, channelInfo *datastore.ChannelInfo) error {
	d := model.Dialog{
		Title: fmt.Sprintf("Add a user to %s", channel.Name),
		Elements: []model.DialogElement{{
			DisplayName: "User",
			Name:        "user",
			Type:        "select",
			DataSource:  "users",
		}, {
			Name:        "grantAdmin",
			DisplayName: "Grant channel admin",
			Placeholder: "Whether the selected user will be granted channel admin.",
			Optional:    true,
			Type:        "bool",
		}},
	}
	dialogRequest, err := p.makeDialogRequest(triggerID, addUserDialogName+"/"+channel.Id, d)
	if err != nil {
		return err
	}
	if err := p.client.Frontend.OpenInteractiveDialog(dialogRequest); err != nil {
		return err
	}

	return nil
}

func (p *Plugin) addUserResponseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json; encoding=utf-8")
	channelID := mux.Vars(r)["channel"]
	ctx := r.Context()
	userID := r.Header.Get("Mattermost-User-ID")
	l := ctxlog.FromContext(ctx).WithField("user", userID).WithField("channel", channelID)

	_, chInfo, err := p.fetchChannelAndCheckPermission(userID, channelID)
	if err != nil {
		l.WithError(err).Errorf("fetching channel information to add a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: err.Error(),
		})
		return
	}

	var payload model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		l.WithError(err).Errorf("parsing dialog payload to add a team to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Invalid dialog payload: " + err.Error(),
		})
		return
	}

	// OK, check if this is going to match any existing ACL entry (and if so, remove it)
	targetUserID := payload.Submission["user"].(string)
	grantAdmin := payload.Submission["grantAdmin"].(bool)

	chInfo.Admins = slices.DeleteFunc(chInfo.Admins, func(e datastore.ACLElement) bool {
		return e.Type == datastore.ACLElementTypeUser && e.Value == targetUserID
	})
	chInfo.Members = slices.DeleteFunc(chInfo.Members, func(e datastore.ACLElement) bool {
		return e.Type == datastore.ACLElementTypeUser && e.Value == targetUserID
	})
	entry := datastore.ACLElement{
		Type:  datastore.ACLElementTypeUser,
		Value: targetUserID,
	}
	if grantAdmin {
		chInfo.Admins = append(chInfo.Admins, entry)
	} else {
		chInfo.Members = append(chInfo.Members, entry)
	}
	if err := p.datastore().SaveChannel(ctx, chInfo); err != nil {
		l.WithError(err).Errorf("saving while adding a user to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Saving new channel ACL: " + err.Error(),
		})
		return
	}

	if err := p.runSync(ctx, "user-acl-add"); err != nil {
		l.WithError(err).Errorf("sync while adding a user to a channel")
		_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{
			Error: "Syncing new channel ACL: " + err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{})
}
