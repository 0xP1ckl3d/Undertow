import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {viewLocations} from './navigation.ts';

test('existing inner-page destinations resolve to their new sidebar groups',()=>{
  for(const view of ['routes','relays','forwards'])assert.equal(viewLocations[view].group,'networking');
  for(const view of ['jobs','transfers','modules'])assert.equal(viewLocations[view].group,'work');
  assert.equal(viewLocations.history.group,'settings');
  assert.equal(viewLocations.history.label,'History');
});

test('every cross-view callback retains a mapped destination',()=>{
  const source=readFileSync(new URL('./main.tsx',import.meta.url),'utf8');
  const destinations=[...source.matchAll(/setView\('([^']+)'\)/g)].map(match=>match[1]);
  assert.ok(destinations.length>0);
  for(const view of destinations)assert.ok(viewLocations[view],`Unmapped link destination: ${view}`);
  for(const view of ['routes','relays','jobs','transfers','settings','deployments','agents','payloads']){
    assert.ok(destinations.includes(view),`Expected cross-view destination: ${view}`);
  }
});

test('ungrouped pages retain their destinations',()=>{
  for(const view of ['topology','agents','deployments','credentials','payloads','team','settings']){
    assert.equal(viewLocations[view].group,view);
  }
});
