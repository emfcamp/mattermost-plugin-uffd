import type {Store, Action} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import UffdGroupTable from './components/uffd_group_table';

import manifest from '@/manifest';
import type {PluginRegistry} from '@/types/mattermost-webapp';

export default class Plugin {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-function
    public async initialize(registry: PluginRegistry, store: Store<GlobalState, Action<Record<string, unknown>>>) {
        // @see https://developers.mattermost.com/extend/plugins/webapp/reference/
        registry.registerSystemConsoleGroupTable(UffdGroupTable);
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
