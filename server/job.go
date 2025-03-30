package main

import (
	"context"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncablesync"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
	log "github.com/sirupsen/logrus"
)

func (p *Plugin) runSyncJob() {
	p.runSync(context.Background(), "schedule")
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
	se := &syncengine.SyncEngine{
		IdP: &syncengine.UffdIdP{
			API:              p.uffd,
			EnabledGroup:     cfg.EnabledGroup,
			GroupFilterRegex: cfg.SyncGroupRegex,
		},
		Service: &syncengine.MattermostService{
			API: p.API,
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
