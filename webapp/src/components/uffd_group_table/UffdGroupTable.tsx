import React from 'react';

import './uffd.scss';

import type {GetGroupsParams, Group} from '@mattermost/types/groups';

import type {ActionResult} from 'mattermost-redux/types/actions';

type Props = {
    groups: Group[];
    actions: {
        getGroups: (opts: GetGroupsParams) => Promise<ActionResult<Group[], any>>;
    };
}

type State = {
    loading: boolean;
    fetchError: boolean;
}

export default class UffdGroupTable extends React.PureComponent<Props, State> {
    constructor(props: Props) {
        super(props);
        this.state = {
            loading: true,
            fetchError: false,
        };
    }

    public componentDidMount() {
        this.props.actions.getGroups({
            page: 0,
            per_page: 1000,
        }).then(this.handleGetGroupsResponse);
    }

    handleGetGroupsResponse = (response: ActionResult) => {
        if (response?.error) {
            this.setState({fetchError: true});
        } else {
            this.setState({fetchError: false});
        }
        this.setState({loading: false});
    };

    render() {
        return (
            <div className={'AdminPanel clearfix'}>
                <div className='header'>
                    <div>
                        <h3>{'uffd Groups'}</h3>
                        <div className='mt-2'>
                            {'Groups synchronized from uffd are listed below. Configure them '}
                            <a href='/admin_console/plugins/plugin_org.emfcamp.mattermost-plugin-uffd'>{'in the uffd plugin settings'}</a>
                            {', and link them to teams and channels in the Teams and Channels settings in the left-hand sidebar.'}
                        </div>
                    </div>
                </div>
                <div className='groups-list'>
                    <div className='groups-list--header'>
                        <div className='group-name'>{'Name'}</div>
                        <div className='group-content'>
                            <div className='group-description'/>
                        </div>
                    </div>
                    <div
                        id='groups-list--body'
                        className='groups-list--body'
                    >
                        {this.renderList()}
                    </div>
                </div>
            </div>
        );
    }

    renderList(): JSX.Element | JSX.Element[] {
        if (this.state.loading) {
            return (
                <div className='groups-list-loading'>
                    <i className='fa fa-spinner fa-pulse fa-2x'/>
                </div>
            );
        }
        if (this.state.fetchError) {
            return (
                <div className='groups-list-empty'>
                    {'Failed to retrieve uffd groups.'}
                </div>
            );
        }
        if (this.props.groups.length === 0) {
            return (
                <div className='groups-list-empty'>
                    {'No groups found.'}
                </div>
            );
        }

        const els: JSX.Element[] = [];
        for (const group of this.props.groups) {
            // TODO(lukegb): extract this into a separate component.
            els.push((
                <div
                    id={`${group.name}_group`}
                    className={'group'}
                >
                    <div className='group-row'>
                        <div className='group-name'>
                            <span>
                                {group.name}
                            </span>
                        </div>
                        <div className='group-content'>
                            <span className='group-description'>
                                {/* TODO(lukegb): list the syncables this is associated with */}
                            </span>
                        </div>
                    </div>
                </div>
            ));
        }
        return els;
    }
}
