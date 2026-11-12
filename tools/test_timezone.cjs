const assert = require('node:assert/strict');
const test = require('node:test');
const { deviceTimezone, timezoneOptions, enhance, useDeviceTimezone } = require('../ui/timezone.js');

test('device timezone is optional and invalid browser values are ignored', () => {
 for (const zone of ['UTC', 'America/Los_Angeles', 'Asia/Kathmandu']) {
  assert.equal(deviceTimezone({ DateTimeFormat: () => ({ resolvedOptions: () => ({ timeZone: zone }) }) }), zone);
 }
 for (const zone of [null, '', 'Local', '../UTC', '<script>']) {
  assert.equal(deviceTimezone({ DateTimeFormat: () => ({ resolvedOptions: () => ({ timeZone: zone }) }) }), null);
 }
 assert.equal(deviceTimezone({}), null);
 assert.deepEqual(timezoneOptions({}), []);
 assert.deepEqual(timezoneOptions({ supportedValuesOf: () => ['Asia/Tokyo', '<script>', 'Local'] }), ['UTC', 'Asia/Tokyo']);
});

test('enhancement preserves the saved value, deduplicates suggestions and survives repeated swaps', () => {
 const input = { value: 'Asia/Kolkata' };
 const button = { hidden: true, dataset: {}, closest: () => ({ querySelector: () => input }) };
 const list = { dataset: {}, options: [{ value: 'UTC' }], ownerDocument: { createElement: () => ({}) }, appendChild(option) { this.options.push(option); } };
 const scope = { querySelectorAll: selector => selector === '[data-device-timezone]' ? [button] : [list] };
 const api = { DateTimeFormat: () => ({ resolvedOptions: () => ({ timeZone: 'America/Los_Angeles' }) }), supportedValuesOf: () => ['UTC', 'America/Los_Angeles', 'Asia/Kolkata', 'America/Los_Angeles'] };
 enhance(scope, api); enhance(scope, api);
 assert.equal(input.value, 'Asia/Kolkata', 'enhancement must never select the device timezone automatically');
 assert.equal(button.hidden, false);
 assert.deepEqual(list.options.map(o => o.value), ['UTC', 'America/Los_Angeles', 'Asia/Kolkata']);
 useDeviceTimezone(button);
 assert.equal(input.value, 'America/Los_Angeles');
 enhance(scope, {});
 assert.equal(button.hidden, true);
 useDeviceTimezone(button);
 assert.equal(input.value, 'America/Los_Angeles');
});
