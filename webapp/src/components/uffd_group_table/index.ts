import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';
import type {Dispatch} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {getGroups as fetchGroups} from 'mattermost-redux/actions/groups';
import {createSelector} from 'mattermost-redux/selectors/create_selector';
import {getAllGroups, getAllCustomGroups} from 'mattermost-redux/selectors/entities/groups';


import UffdGroupTable from './UffdGroupTable';

const getSortedListOfGroups = createSelector(
    'getSortedListOfGroups',
    getAllGroups,
    (groupsRecords) => {
        const groups = Object.values(groupsRecords).filter((a) => a.source === 'plugin_uffd');
        groups.sort((a, b) => a.name.localeCompare(b.name));
        return groups;
    },
);

function mapStateToProps(state: GlobalState) {
    return {
        groups: getSortedListOfGroups(state),
    };
}

function mapDispatchToProps(dispatch: Dispatch) {
    return {
        actions: bindActionCreators({
            getGroups: fetchGroups,
        }, dispatch),
    };
}

export default connect(mapStateToProps, mapDispatchToProps)(UffdGroupTable);
