# UFFD Plugin

This is a plugin for better supporting group synchronisation between
[uffd](https://git.cccv.de/uffd/uffd/) and Mattermost.

Because of limitations in the Professional tier license that we got from
Mattermost, this doesn't use the Mattermost-native group mechanism. Instead,
it reimplements bits of it itself.

It:

* Creates users if they exist in uffd
* Deactivates users if they stop being returned by uffd (e.g. you have "Hide
  deactivated users from service" enabled for the service and deactivate a uffd
  user)
* Syncs group membership lists into a plugin-internal list
* Syncs groups from its internal store into the System Administrator role
* Syncs groups from its internal store into memberships of channels

It has a specific idea of what groups should be to match the way EMF works, so
likely isn't broadly applicable:

* `team_$FOO` indicates the members of a team named '$FOO'
* `moderation_$FOO` indicates the team leads of the '$FOO' team

$FOO team will automatically get two channels:

* `$FOO`, a public channel
* `$FOO-private`, a private channel

Team members (and leads) of a $FOO team are:

* Automatically made members of `$FOO` and `$FOO-`-prefixed public channels
* Automatically made members of `$FOO-`-prefixed private channels

Team leads of the '$FOO' team are granted:

* Channel administrator on all channels named `$FOO` or beginning `$FOO-`
* The ability to use a bot command to create a new (public, private, unmanaged)
  `$FOO-` prefixed channel
    * Unmanaged is a private channel which only has the team leads automatically
      added, but will not automatically remove any members (i.e. team members don't
      get added automatically)

Note that when teams are removed, the plugin will not automatically clean up related
channels - this must be done manually by a System Administrator.