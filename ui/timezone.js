/* SPDX-License-Identifier: AGPL-3.0-or-later */
(function (root) {
	const zoneName = /^(UTC|[A-Za-z0-9_+-]+(\/[A-Za-z0-9_+-]+)+)$/;
	function deviceTimezone(api) {
		try {
			const zone = api.DateTimeFormat().resolvedOptions().timeZone;
			return typeof zone === 'string' && zone.length <= 64 && zoneName.test(zone) ? zone : null;
		} catch (_) { return null; }
	}
	function timezoneOptions(api) {
		try {
			return ['UTC'].concat(api.supportedValuesOf('timeZone')).filter(zone => typeof zone === 'string' && zone.length <= 64 && zoneName.test(zone));
		} catch (_) { return []; }
	}
	function enhance(scope, api) {
		const zone = deviceTimezone(api);
		scope.querySelectorAll('[data-device-timezone]').forEach(button => {
			button.hidden = !zone;
			button.dataset.deviceTimezone = zone || '';
		});
		const zones = timezoneOptions(api);
		scope.querySelectorAll('datalist#timezones').forEach(list => {
			if (list.dataset.enhanced || !zones.length) return;
			const known = new Set(Array.from(list.options, option => option.value));
			zones.forEach(zone => {
				if (known.has(zone)) return;
				const option = list.ownerDocument.createElement('option');
				option.value = zone;
				list.appendChild(option);
				known.add(zone);
			});
			list.dataset.enhanced = 'true';
		});
	}
	function useDeviceTimezone(button) {
		const input = button.closest('form').querySelector('[name="timezone"]');
		const zone = button.dataset.deviceTimezone;
		if (input && zone && zoneName.test(zone)) input.value = zone;
	}
	if (typeof module !== 'undefined') module.exports = { deviceTimezone, timezoneOptions, enhance, useDeviceTimezone };
	if (root.document) {
		root.document.addEventListener('DOMContentLoaded', () => enhance(root.document, root.Intl));
		root.document.addEventListener('htmx:afterSwap', () => enhance(root.document, root.Intl));
		root.document.addEventListener('click', event => {
			const button = event.target.closest('[data-device-timezone]');
			if (button) useDeviceTimezone(button);
		});
	}
})(typeof window !== 'undefined' ? window : globalThis);
