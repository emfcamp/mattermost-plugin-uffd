package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lukegb/mattermost-plugin-uffd/server/command"
	"github.com/lukegb/mattermost-plugin-uffd/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// kvstore is the client used to read/write KV records for this plugin.
	kvstore kvstore.KVStore

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// commandClient is the client used to register and execute slash commands.
	commandClient command.Command

	// uffd is the Uffd API.
	uffd *UffdAPI

	syncJob   *cluster.Job
	syncMutex *cluster.Mutex

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
}

func (p *Plugin) rescheduleSync() error {
	if p.client == nil {
		return nil // not yet; maybe the first OnConfigurationChange before OnActivate?
	}

	if p.syncJob != nil {
		if err := p.syncJob.Close(); err != nil {
			p.API.LogError("Failed to close sync job", "err", err)
		}
	}

	job, err := cluster.Schedule(
		p.API,
		"SyncJob",
		cluster.MakeWaitForRoundedInterval(time.Duration(p.getConfiguration().SyncInterval)),
		p.runSyncJob,
	)
	if err != nil {
		return fmt.Errorf("failed to schedule sync job: %w", err)
	}

	p.syncJob = job

	return nil
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	p.kvstore = kvstore.NewKVStore(p.client)

	p.commandClient = command.NewCommandHandler(p.client)

	cfg := p.getConfiguration()
	p.uffd = &UffdAPI{
		HTTPClient:   http.DefaultClient,
		EndpointBase: cfg.UffdAddress,
		Username:     cfg.UffdApiUser,
		Password:     cfg.UffdApiPassword,
	}

	if err := p.rescheduleSync(); err != nil {
		return fmt.Errorf("rescheduleSync: %w", err)
	}

	syncMutex, err := cluster.NewMutex(
		p.API,
		"SyncMutex",
	)
	if err != nil {
		return fmt.Errorf("creating SyncMutex: %w", err)
	}
	p.syncMutex = syncMutex

	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	if p.syncJob != nil {
		if err := p.syncJob.Close(); err != nil {
			p.API.LogError("Failed to close sync job", "err", err)
		}
	}
	return nil
}

// This will execute the commands that were registered in the NewCommandHandler function.
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response, err := p.commandClient.Handle(args)
	if err != nil {
		return nil, model.NewAppError("ExecuteCommand", "plugin.command.execute_command.app_error", nil, err.Error(), http.StatusInternalServerError)
	}
	return response, nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
