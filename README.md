# UFFD Plugin

This is a plugin for better supporting group synchronisation between
[uffd](https://git.cccv.de/uffd/uffd/) and Mattermost.

It:

* Creates users if they exist in uffd
* Deactivates users if they stop being returned by uffd (e.g. you have "Hide
  deactivated users from service" enabled for the service and deactivate a uffd user)
* Syncs groups from uffd into Mattermost
