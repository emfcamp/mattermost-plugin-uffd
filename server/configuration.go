package main

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/pkg/errors"
)

type configurationDuration time.Duration

func (c configurationDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(c).String())
}

func (c *configurationDuration) UnmarshalJSON(data []byte) error {
	var ds string
	if err := json.Unmarshal(data, &ds); err != nil {
		return err
	}
	d, err := time.ParseDuration(ds)
	if err != nil {
		return err
	}
	*c = configurationDuration(d)
	return nil
}

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	// EnabledGroup defines the name of a group that, if a user is present in it,
	// causes the user to be enabled for Mattermost.
	// This should usually match how the OIDC service is configured in uffd.
	// If empty, Mattermost users will only be disabled if they are disabled in uffd (and not if they no longer have access to Mattermost).
	EnabledGroup string

	// SystemAdminGroup defines the name of a group that will be synced to the system_admin role.
	SystemAdminGroup string

	// ManagedTeam is the slug name of the team that will be managed by the plugin.
	ManagedTeam string

	// SyncInterval is the interval at which groups will be synced. Note
	// that syncing can also be triggered by hitting the "sync" endpoint.
	SyncInterval configurationDuration

	// UffdAddress is the address of the UFFD instance (the base), from
	// which the API endpoints will be derived.
	UffdAddress string

	// UffdAPIUser is the username used for authenticating with the UFFD API.
	UffdAPIUser string

	// UffdAPIPassword is the password used for authenticating with the UFFD API.
	UffdAPIPassword string
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *configuration) Clone() *configuration {
	var clone = *c
	return &clone
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	p.setConfiguration(configuration)

	if err := p.rescheduleSync(); err != nil {
		return errors.Wrap(err, "setting up sync scheduled job")
	}

	return nil
}
