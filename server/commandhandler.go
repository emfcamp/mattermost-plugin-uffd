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
	renameTrigger = "rename"
	createTrigger = "create"
)

type CommandHandler struct {
	// client is the Mattermost server API client.
	client *pluginapi.Client
	api    plugin.API

	runSync func(ctx context.Context, trigger string) error
}

func NewCommandHandler(p *Plugin) (*CommandHandler, error) {
	c := &CommandHandler{
		client:  pluginapi.NewClient(p.API, p.Driver),
		api:     p.API,
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
		createACD := model.NewAutocompleteData(createTrigger, "[name] [type]", "Create a new public/private channel")
		createACD.AddTextArgument("[name]", "[text]", "")
		createACD.AddTextArgument("[type]", "[public/private/unmanaged-private]", "unmanaged-private channels do not automatically add team members")
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

	foundTeam, err := teamFromChannelName(ctx, ds, newName)
	if err != nil {
		return errResponsef("An error occurred while checking for the team the channel belongs to: %v", err)
	} else if foundTeam == nil {
		return errResponsef("Channel names need to be begin with a team name")
	} else if ch.Name == foundTeam.Name || ch.Name == foundTeam.Name+"-private" {
		return errResponsef("You can't rename the team default public or private channels")
	} else if !h.authorizedForTeam(foundTeam, args.UserId) {
		return errResponsef("You aren't a team lead of %s, so you can't rename channels to belong with that name.", foundTeam.Name)
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

	oldName := ch.Name
	ch.Name = newName
	ch.DisplayName = newName
	if err := h.client.Channel.Update(ch); err != nil {
		return errResponsef("Renaming the channel failed: %v", err)
	}

	// If we renamed across teams then we need to run a sync.
	if !strings.HasPrefix(oldName, foundTeam.Name) {
		if err := h.runSync(ctx, "channel-rename"); err != nil {
			return errResponsef("An error occurred syncing membership information: %v", err)
		}
	}

	return &model.CommandResponse{}, nil
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
	if !map[string]bool{"public": true, "private": true, "unmanaged-private": true}[newType] {
		return errResponsef("Channel type should be one of public, private, or unmanaged-private")
	}

	ds := &datastore.MattermostDataStore{API: h.api}

	foundTeam, err := teamFromChannelName(ctx, ds, newName)
	if err != nil {
		return errResponsef("An error occurred while checking for the team the channel belongs to: %v", err)
	} else if foundTeam == nil {
		return errResponsef("Channel names need to be begin with a team name")
	} else if !h.authorizedForTeam(foundTeam, args.UserId) {
		return errResponsef("You aren't a team lead of %s, so you can't create channels that start with that name.", foundTeam.Name)
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
		MembershipUnmanaged: newType == "unmanaged-private",
	}
	if err := ds.SaveChannel(ctx, chInfo); err != nil {
		return errResponsef("An error occurred saving additional channel information: %v", err)
	}

	if err := h.runSync(ctx, "channel-create"); err != nil {
		return errResponsef("An error occurred syncing initial membership information: %v", err)
	}

	return &model.CommandResponse{}, nil
}
