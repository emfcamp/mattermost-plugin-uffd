package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/lukegb/mattermost-plugin-uffd/server/datastore"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	renameTrigger     = "rename"
	createTrigger     = "create"
	addTeamTrigger    = "addteam"
	removeTeamTrigger = "removeteam"
	addUserTrigger    = "adduser"
	removeUserTrigger = "removeuser"
)

type CommandHandler struct {
	// client is the Mattermost server API client.
	client *pluginapi.Client
	api    plugin.API
	plugin *Plugin

	runSync func(ctx context.Context, trigger string) error
}

func NewCommandHandler(p *Plugin) (*CommandHandler, error) {
	c := &CommandHandler{
		client:  pluginapi.NewClient(p.API, p.Driver),
		api:     p.API,
		plugin:  p,
		runSync: p.runSync,
	}

	{
		renameACD := model.NewAutocompleteData(renameTrigger, "[new-name]", "Rename the channel")
		renameACD.AddTextArgument("[new-name]", "[text]", "")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          renameTrigger,
			DisplayName:      "rename",
			AutoComplete:     true,
			AutoCompleteDesc: "Rename the channel",
			AutoCompleteHint: "[new-name]",
			AutocompleteData: renameACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", renameTrigger, err)
		}
	}

	{
		createACD := model.NewAutocompleteData(createTrigger, "[name] [type]", "Create a new channel")
		createACD.AddTextArgument("[name]", "[text]", "")
		createACD.AddTextArgument("[type]", "[public/team/leads/empty]", "")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          createTrigger,
			DisplayName:      "create",
			AutoComplete:     true,
			AutoCompleteDesc: "Create a new channel",
			AutoCompleteHint: "[name] [type]",
			AutocompleteData: createACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", createTrigger, err)
		}
	}

	{
		addTeamACD := model.NewAutocompleteData(addTeamTrigger, "", "Add team to the current channel")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          addTeamTrigger,
			DisplayName:      "addTeam",
			AutoComplete:     true,
			AutoCompleteDesc: "Add team to the current channel",
			AutoCompleteHint: "",
			AutocompleteData: addTeamACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", addTeamTrigger, err)
		}
	}
	{
		removeTeamACD := model.NewAutocompleteData(removeTeamTrigger, "", "Remove team from the current channel")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          removeTeamTrigger,
			DisplayName:      "removeTeam",
			AutoComplete:     true,
			AutoCompleteDesc: "Remove team from the current channel",
			AutoCompleteHint: "",
			AutocompleteData: removeTeamACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", removeTeamTrigger, err)
		}
	}

	{
		addUserACD := model.NewAutocompleteData(addUserTrigger, "", "Add user to the current channel")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          addUserTrigger,
			DisplayName:      "addUser",
			AutoComplete:     true,
			AutoCompleteDesc: "Add user to the current channel",
			AutoCompleteHint: "",
			AutocompleteData: addUserACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", addUserTrigger, err)
		}
	}
	{
		removeUserACD := model.NewAutocompleteData(removeUserTrigger, "", "Remove user from the current channel")
		if err := c.client.SlashCommand.Register(&model.Command{
			Trigger:          removeUserTrigger,
			DisplayName:      "removeUser",
			AutoComplete:     true,
			AutoCompleteDesc: "Remove user from the current channel",
			AutoCompleteHint: "",
			AutocompleteData: removeUserACD,
		}); err != nil {
			return nil, fmt.Errorf("creating /%s: %w", removeUserTrigger, err)
		}
	}

	return c, nil
}

func (h *CommandHandler) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := strings.TrimPrefix(strings.Fields(args.Command)[0], "/")
	switch trigger {
	case renameTrigger:
		return h.executeRename(ctx, c, args)
	case createTrigger:
		return h.executeCreate(ctx, c, args)
	case addTeamTrigger:
		return h.executeAddTeam(ctx, c, args)
	case addUserTrigger:
		return h.executeAddUser(ctx, c, args)
	case removeTeamTrigger, removeUserTrigger:
		return h.executeRemoveMember(ctx, c, args)
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown command: %s", args.Command),
		}, nil
	}
}

func errResponsef(f string, args ...any) (*model.CommandResponse, *model.AppError) {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf(f, args...),
	}, nil
}

func (h *CommandHandler) authorizedForTeam(team *syncengine.Team, userID string) bool {
	if stringset.FromSlice(team.Leads).Contains(userID) {
		return true
	}
	if h.client.User.HasPermissionTo(userID, model.PermissionSysconsoleWriteUserManagementTeams) {
		return true
	}
	return false
}

func (h *CommandHandler) executeRename(ctx context.Context, c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	bits := strings.Fields(args.Command)
	if len(bits) != 2 {
		return errResponsef("Command syntax is /%s [new-name]", renameTrigger)
	}

	ch, err := h.client.Channel.Get(args.ChannelId)
	if err != nil {
		return errResponsef("Your current channel is invalid.")
	}

	permissionRequired := model.PermissionManagePublicChannelProperties
	switch ch.Type {
	case model.ChannelTypeOpen:
		// Default.
	case model.ChannelTypePrivate:
		permissionRequired = model.PermissionManagePrivateChannelProperties
	default:
		return errResponsef("You must be in a public/private channel to use this command.")
	}
	if !h.client.User.HasPermissionToChannel(args.UserId, args.ChannelId, permissionRequired) {
		return errResponsef("You don't have permission to rename this channel.")
	}

	newName := bits[1]
	if len(newName) > model.ChannelNameMaxLength || len(newName) < model.ChannelNameMinLength {
		return errResponsef("Channel names must be between %d and %d characters long (%s is %d characters long)", model.ChannelNameMinLength, model.ChannelNameMaxLength, newName, len(newName))
	}

	ds := &datastore.MattermostDataStore{API: h.api}

	if h.client.User.HasPermissionTo(args.UserId, model.PermissionManageSystem) {
		// Superusers can do whatever they want.
		chInfo, ok, err := ds.LoadChannel(ctx, ch.Id)
		if err != nil {
			return errResponsef("An error occurred while loading the channel's information: %v", err)
		}
		if ok {
			chInfo.Name = newName
			if err := ds.SaveChannel(ctx, chInfo); err != nil {
				return errResponsef("An error occurred while saving the channel's information: %v", err)
			}
		}

		ch.Name = newName
		ch.DisplayName = newName
		if err := h.client.Channel.Update(ch); err != nil {
			return errResponsef("Renaming the channel failed: %v", err)
		}
		return &model.CommandResponse{}, nil
	}

	previousTeam, _ := teamFromChannelName(ctx, ds, ch.Name)
	var foundTeam *syncengine.Team
	if !isFreeForAllNamespace(newName) {
		var err error
		foundTeam, err = teamFromChannelName(ctx, ds, newName)
		switch {
		case err != nil:
			return errResponsef("An error occurred while checking for the team the channel belongs to: %v", err)
		case foundTeam == nil:
			return errResponsef("Channel names need to begin with a team name")
		case ch.Name == foundTeam.Name, ch.Name == foundTeam.Name+"-private":
			return errResponsef("You can't rename the team default public or private channels")
		case !h.authorizedForTeam(foundTeam, args.UserId):
			return errResponsef("You aren't a team lead of %s, so you can't rename channels to begin with that name.", foundTeam.Name)
		}
	}

	chInfo, ok, err := ds.LoadChannel(ctx, ch.Id)
	if err != nil {
		return errResponsef("An error occurred while loading the channel's information: %v", err)
	}
	if ok {
		chInfo.Name = newName
		if err := ds.SaveChannel(ctx, chInfo); err != nil {
			return errResponsef("An error occurred while saving the channel's information: %v", err)
		}
	}

	ch.Name = newName
	ch.DisplayName = newName
	if err := h.client.Channel.Update(ch); err != nil {
		return errResponsef("Renaming the channel failed: %v", err)
	}

	// If we renamed across teams then we need to run a sync.
	teamsEqual := func(t1, t2 *syncengine.Team) bool {
		switch {
		case (t1 == nil) != (t2 == nil):
			// nilness is different, they're different
			return false
		case t1 == nil, t2 == nil:
			// they're both nil
			return true
		}
		return t1.Name != t2.Name
	}
	if !teamsEqual(foundTeam, previousTeam) {
		if err := h.runSync(ctx, "channel-rename"); err != nil {
			return errResponsef("An error occurred syncing membership information: %v", err)
		}
	}

	return &model.CommandResponse{}, nil
}

func isFreeForAllNamespace(name string) bool {
	return strings.HasPrefix(name, "misc-")
}

func teamFromChannelName(ctx context.Context, ds *datastore.MattermostDataStore, name string) (*syncengine.Team, error) {
	segments := strings.Split(name, "-")
	for n := 1; n <= len(segments); n++ {
		prefix := strings.Join(segments[:n], "-")
		// Is this a valid team name?
		team, ok, err := ds.LoadTeam(ctx, prefix)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		return &team, nil
	}
	return nil, nil
}

func (h *CommandHandler) executeCreate(ctx context.Context, c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	bits := strings.Fields(args.Command)
	if len(bits) != 3 {
		return errResponsef("Command syntax is /%s [name] [type]", createTrigger)
	}

	newName := bits[1]
	if len(newName) > model.ChannelNameMaxLength || len(newName) < model.ChannelNameMinLength {
		return errResponsef("Channel names must be between %d and %d characters long (%s is %d characters long)", model.ChannelNameMinLength, model.ChannelNameMaxLength, newName, len(newName))
	}

	newType := bits[2]
	if !map[string]bool{"public": true, "team": true, "leads": true, "empty": true}[newType] {
		return errResponsef("Channel type should be one of public, team, leads or empty")
	}

	ds := &datastore.MattermostDataStore{API: h.api}

	var foundTeam *syncengine.Team
	if !isFreeForAllNamespace(newName) {
		var err error
		foundTeam, err = teamFromChannelName(ctx, ds, newName)
		switch {
		case err != nil:
			return errResponsef("An error occurred while checking for the team the channel belongs to: %v", err)
		case foundTeam == nil:
			return errResponsef("Channel names need to begin with a team name")
		case !h.authorizedForTeam(foundTeam, args.UserId):
			return errResponsef("You aren't a team lead of %s, so you can't create channels that start with that name.", foundTeam.Name)
		}
	}

	ch := &model.Channel{
		TeamId:      args.TeamId,
		Type:        model.ChannelTypePrivate,
		Name:        newName,
		DisplayName: newName,
		CreatorId:   args.UserId,
	}
	if newType == "public" {
		ch.Type = model.ChannelTypeOpen
	}
	if err := h.client.Channel.Create(ch); err != nil {
		return errResponsef("An error occurred while creating the channel: %v", err)
	}

	chInfo := &datastore.ChannelInfo{
		ID:                  ch.Id,
		Name:                newName,
		MembershipUnmanaged: false,
	}
	switch newType {
	case "empty":
		// 'empty' channels contain only the creator.
		chInfo.Admins = []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeUser,
			Value: args.UserId,
		}}
	case "leads":
		// 'leads' channels contain the team leads only.
		if foundTeam == nil {
			return errResponsef("'leads' type channels must begin with the name of a team")
		}
		chInfo.Admins = []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeTeamLead,
			Value: foundTeam.Name,
		}}
	case "team":
		// 'team' channels contain leads and the team members.
		if foundTeam == nil {
			return errResponsef("'team' type channels must begin with the name of a team")
		}
		chInfo.Admins = []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeTeamLead,
			Value: foundTeam.Name,
		}}
		chInfo.Members = []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeTeamMember,
			Value: foundTeam.Name,
		}}
	case "public":
		if foundTeam != nil {
			// This channel belongs to a team.
			chInfo.Admins = []datastore.ACLElement{{
				Type:  datastore.ACLElementTypeTeamLead,
				Value: foundTeam.Name,
			}}
			chInfo.Members = []datastore.ACLElement{{
				Type:  datastore.ACLElementTypeTeamMember,
				Value: foundTeam.Name,
			}}
		} else {
			// This channel is teamless.
			chInfo.Admins = []datastore.ACLElement{{
				Type:  datastore.ACLElementTypeUser,
				Value: args.UserId,
			}}
		}
	}
	if err := ds.SaveChannel(ctx, chInfo); err != nil {
		return errResponsef("An error occurred saving additional channel information: %v", err)
	}

	if err := h.runSync(ctx, "channel-create"); err != nil {
		return errResponsef("An error occurred syncing initial membership information: %v", err)
	}

	return &model.CommandResponse{}, nil
}

type permissionError string

func (e permissionError) Error() string { return string(e) }

func (p *Plugin) fetchChannelAndCheckPermission(userID, channelID string) (*model.Channel, *datastore.ChannelInfo, error) {
	ctx := context.TODO()

	ch, err := p.client.Channel.Get(channelID)
	if err != nil {
		return nil, nil, permissionError("Your current channel is invalid.")
	}

	permissionRequired := model.PermissionManagePublicChannelMembers
	switch ch.Type {
	case model.ChannelTypeOpen:
		// Default.
	case model.ChannelTypePrivate:
		permissionRequired = model.PermissionManagePrivateChannelMembers
	default:
		return nil, nil, permissionError("You must be in a public/private channel to use this command.")
	}
	if !p.client.User.HasPermissionToChannel(userID, channelID, permissionRequired) {
		return nil, nil, permissionError("You don't have permission to manage this channel.")
	}

	chInfo, ok, err := p.datastore().LoadChannel(ctx, channelID)
	if err != nil {
		return nil, nil, permissionError(fmt.Sprintf("Loading channel plugin metadata failed: %v", err))
	} else if !ok {
		// Default some channel info to avoid mystery crashing.
		chInfo = &datastore.ChannelInfo{
			ID:                  ch.Id,
			Name:                ch.Name,
			MembershipUnmanaged: true,
		}
		if err := p.datastore().SaveChannel(ctx, chInfo); err != nil {
			return nil, nil, permissionError(fmt.Sprintf("Setting up default channel metadata: %v", err))
		}
	}
	return ch, chInfo, nil
}

func (h *CommandHandler) executeAddTeam(ctx context.Context, c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	bits := strings.Fields(args.Command)
	if len(bits) != 1 {
		return errResponsef("Command syntax is /%s", addTeamTrigger)
	}

	ch, chInfo, err := h.plugin.fetchChannelAndCheckPermission(args.UserId, args.ChannelId)
	if err != nil {
		return errResponsef("%s", err)
	}

	if err := h.plugin.openAddTeamDialog(ctx, args.TriggerId, ch, chInfo); err != nil {
		return errResponsef("%s", err)
	}

	return &model.CommandResponse{}, nil
}

func (h *CommandHandler) executeRemoveMember(ctx context.Context, c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	bits := strings.Fields(args.Command)
	if len(bits) != 1 {
		return errResponsef("Command syntax is /%s", bits[0])
	}

	ch, chInfo, err := h.plugin.fetchChannelAndCheckPermission(args.UserId, args.ChannelId)
	if err != nil {
		return errResponsef("%s", err)
	}

	if err := h.plugin.openRemoveMemberDialog(ctx, args.TriggerId, ch, chInfo); err != nil {
		return errResponsef("%s", err)
	}

	return &model.CommandResponse{}, nil
}

func (h *CommandHandler) executeAddUser(ctx context.Context, c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	bits := strings.Fields(args.Command)
	if len(bits) != 1 {
		return errResponsef("Command syntax is /%s", addUserTrigger)
	}

	ch, chInfo, err := h.plugin.fetchChannelAndCheckPermission(args.UserId, args.ChannelId)
	if err != nil {
		return errResponsef("%s", err)
	}

	if err := h.plugin.openAddUserDialog(ctx, args.TriggerId, ch, chInfo); err != nil {
		return errResponsef("%s", err)
	}

	return &model.CommandResponse{}, nil
}
