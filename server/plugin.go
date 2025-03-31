package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
	"github.com/lukegb/mattermost-plugin-uffd/server/uffd"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	log "github.com/sirupsen/logrus"
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// uffd is the Uffd API.
	uffd *uffd.API

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
			p.MattermostPlugin.API.LogError("Failed to close sync job", "err", err)
		}
	}

	job, err := cluster.Schedule(
		p.MattermostPlugin.API,
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
	p.client = pluginapi.NewClient(p.MattermostPlugin.API, p.MattermostPlugin.Driver)

	pluginapi.ConfigureLogrus(log.StandardLogger(), p.client)

	cfg := p.getConfiguration()
	p.uffd = &uffd.API{
		HTTPClient:   http.DefaultClient,
		EndpointBase: cfg.UffdAddress,
		Username:     cfg.UffdApiUser,
		Password:     cfg.UffdApiPassword,
	}

	if err := p.rescheduleSync(); err != nil {
		return fmt.Errorf("rescheduleSync: %w", err)
	}

	syncMutex, err := cluster.NewMutex(
		p.MattermostPlugin.API,
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
			log.WithFields(log.Fields{"err": err}).Error("Failed to close sync job")
		}
	}
	return nil
}

// UserWillLogIn triggers before the user logs in.
func (p *Plugin) UserWillLogIn(c *plugin.Context, user *model.User) string {
	if user.Props != nil && user.Props[syncengine.MMIdPUsernameProp] != user.Username {
		user.Username = user.Props[syncengine.MMIdPUsernameProp]
		var appErr *model.AppError
		user, appErr = p.MattermostPlugin.API.UpdateUser(user)
		if appErr != nil {
			log.WithFields(log.Fields{
				"user": user,
				"err":  appErr,
			}).Error("Updating username on login failed")
		}
	}
	return "" // empty string permits login
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
