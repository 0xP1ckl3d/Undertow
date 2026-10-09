export type View='topology'|'agents'|'deployments'|'credentials'|'jobs'|'transfers'|'routes'|'relays'|'forwards'|'modules'|'payloads'|'team'|'history'|'settings';
export type NavGroup='topology'|'agents'|'deployments'|'credentials'|'networking'|'work'|'payloads'|'team'|'settings';
export type SettingsSection='Client'|'Carriers'|'Agents'|'Identity'|'Status'|'Logs'|'History';

// Keep leaf destinations stable so every existing cross-view link still opens
// its exact page, while the sidebar and breadcrumbs use the grouped location.
export const viewLocations:Record<View,{group:NavGroup;label:string}>={
  topology:{group:'topology',label:'Topology'},
  agents:{group:'agents',label:'Agents'},
  deployments:{group:'deployments',label:'Jump'},
  credentials:{group:'credentials',label:'Credentials'},
  routes:{group:'networking',label:'Routes'},
  relays:{group:'networking',label:'Relays'},
  forwards:{group:'networking',label:'Forwards'},
  jobs:{group:'work',label:'Jobs'},
  transfers:{group:'work',label:'Transfers'},
  modules:{group:'work',label:'Modules'},
  payloads:{group:'payloads',label:'Payloads'},
  team:{group:'team',label:'Team'},
  history:{group:'settings',label:'History'},
  settings:{group:'settings',label:'Settings'},
};

export const settingsLabels:Record<SettingsSection,string>={
  Client:'Client',Carriers:'Carriers',Agents:'Agents',Identity:'Identity',
  Status:'Connection status',Logs:'Server logs',History:'History',
};
