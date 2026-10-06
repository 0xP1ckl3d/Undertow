import assert from 'node:assert/strict';
import test from 'node:test';
import {endpointParts} from './payloadEndpoint.ts';

test('saved direct carrier endpoints retain their ports',()=>{
  assert.deepEqual(endpointParts('13.210.247.60:443'),{host:'13.210.247.60',port:'443'});
  assert.deepEqual(endpointParts('13.210.247.60:53'),{host:'13.210.247.60',port:'53'});
  assert.deepEqual(endpointParts('server.example.com:443'),{host:'server.example.com',port:'443'});
});

test('listener IPv6 binds and host-only profile inputs',()=>{
  assert.deepEqual(endpointParts('[::]:443'),{host:'::',port:'443'});
  assert.deepEqual(endpointParts('[2001:db8::1]:8443'),{host:'2001:db8::1',port:'8443'});
  assert.deepEqual(endpointParts('2001:db8::1'),{host:'2001:db8::1',port:''});
  assert.deepEqual(endpointParts('13.210.247.60'),{host:'13.210.247.60',port:''});
});
