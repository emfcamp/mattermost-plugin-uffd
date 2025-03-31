// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from "react";

export interface PluginRegistry {
    registerPostTypeComponent(typeName: string, component: React.ElementType);
    registerSystemConsoleGroupTable(component: React.ElementType);

    // Add more if needed from https://developers.mattermost.com/extend/plugins/webapp/reference
}
