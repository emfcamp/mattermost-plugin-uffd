package main

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	log "github.com/sirupsen/logrus"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncablesync"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
)

func (p *Plugin) runSyncJob() {
	_ = p.runSync(context.Background(), "schedule")
}

func (p *Plugin) runSync(ctx context.Context, trigger string) error {
	l := log.WithFields(log.Fields{
		"trigger": trigger,
	})
	ctx = ctxlog.NewContext(ctx, l)

	l.Debug("Acquiring UFFD sync mutex")
	p.syncMutex.Lock()
	defer p.syncMutex.Unlock()

	cfg := p.getConfiguration()

	l.Info("Performing UFFD group sync")
	var groupBackend syncengine.MattermostGroupBackend = &syncengine.MattermostPluginGroupBackend{
		API: p.API,
	}
	ftr := p.API.GetLicense().Features
	if ftr == nil || ftr.LDAPGroups == nil || *ftr.LDAPGroups == false {
		l.Warning("Force-enabling custom-groups syncing: installed license does not have LDAP groups support!")
		cfg.SyncAsCustomGroups = true
	}
	if cfg.SyncAsCustomGroups {
		l.Info("Syncing as custom groups")
		siteURL := p.API.GetConfig().ServiceSettings.SiteURL
		if siteURL == nil {
			return fmt.Errorf("site url setting is missing")
		}
		groupBackend = &syncengine.MattermostRESTGroupBackend{
			PluginAPI: p.API,
			RESTAPI:   model.NewAPIv4Client(*siteURL),
		}
	}

	se := &syncengine.SyncEngine{
		IDP: &syncengine.UffdIDP{
			API:              p.uffd,
			EnabledGroup:     cfg.EnabledGroup,
			GroupFilterRegex: cfg.SyncGroupRegex,
		},
		Service: &syncengine.MattermostService{
			MattermostGroupBackend: groupBackend,
			API:                    p.API,
		},
	}
	out, err := se.FullSync(ctx)
	if err != nil {
		l.WithError(err).Error("UFFD group sync failed")
		return err
	}
	l.WithFields(log.Fields{"outcome": out}).Info("UFFD group sync complete")

	l.Info("Performing syncables sync")
	ss := &syncablesync.Engine{
		API: &syncablesync.Mattermost{
			API:                p.API,
			SystemAdminGroup:   cfg.SystemAdminGroup,
			SystemManagerGroup: cfg.SystemManagerGroup,
		},
	}
	if err := ss.FullSync(ctx); err != nil {
		l.WithError(err).Error("Syncables sync failed")
	}
	l.Info("Syncables sync complete")

	return nil
}
