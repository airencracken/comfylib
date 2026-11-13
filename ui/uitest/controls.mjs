// Open native disclosure controls using the same interaction as a reader.
export async function act(locator, method, ...args) {
  const page = locator.page();
  if (await locator.count() === 0) {
    const menu = page.locator('details.comfy-account:not([open]) > summary');
    if (await menu.count()) await menu.click();
  }
  if (await locator.count() && !await locator.locator('xpath=ancestor::details[contains(@class, \"comfy-menu\")]').count()) {
    const openMenu = page.locator('details.comfy-menu[open] > summary');
    for (let i = await openMenu.count()-1; i >= 0; i--) await openMenu.nth(i).click();
  }
  const ancestors = locator.locator('xpath=ancestor::details[not(@open)]');
  for (let i = await ancestors.count() - 1; i >= 0; i--) {
    await ancestors.nth(i).locator(':scope > summary').click();
  }
  return locator[method](...args);
}

export async function checkSuiteControls(page, { javascript = true, density = false, apps = false } = {}) {
  const expect = (value, message) => { if (!value) throw new Error(message); };
  const account = page.locator('details.comfy-account');
  expect(await account.count() === 1, 'One Account menu groups secondary actions');
  if (await account.getAttribute('open') !== null) await account.locator(':scope > summary').click();
  expect(!await account.locator('a').first().isVisible(), 'Account actions start collapsed');
  await account.locator(':scope > summary').focus();
  await page.keyboard.press('Enter');
  expect(await account.locator('a').first().isVisible(), 'Account menu opens from the keyboard');
  if (javascript) {
    await page.keyboard.press('Escape');
    expect(await account.getAttribute('open') === null, 'Escape dismisses Account');
    expect(await account.locator(':scope > summary').evaluate(element => element === document.activeElement), 'Escape restores focus');
  } else await account.locator(':scope > summary').click();
  if (apps) {
    const menu = page.locator('details.comfy-apps');
    await menu.locator(':scope > summary').click();
    expect(await menu.locator('a[href="https://suite.example.org"]').isVisible(), 'Configured Apps destination is available');
    await menu.locator(':scope > summary').click();
  }
  if (density) {
    const controls = page.locator('[data-density-controls]');
    expect(await controls.isVisible() === javascript, 'Density progressively enhances the default view');
    if (javascript) {
      await controls.locator('[data-density-choice="compact"]').click();
      expect(await page.locator('html').getAttribute('data-density') === 'compact', 'Compact reading applies');
      await page.reload();
      expect(await page.locator('html').getAttribute('data-density') === 'compact', 'Compact reading survives reload');
      expect(await page.locator('[data-density-choice="compact"]').getAttribute('aria-pressed') === 'true', 'Selected density is announced');
      await page.locator('[data-density-choice="comfortable"]').click();
    }
  }
  await checkPaletteTypography(page);
}

// Color choices must preserve each app's fonts and heading weights.
export async function checkPaletteTypography(page) {
  const result = await page.evaluate(() => {
    const root = document.documentElement;
    const original = ['data-palette', 'data-theme'].map(name => [name, root.getAttribute(name)]);
    const elements = [...document.querySelectorAll('body, h1, h2, h3, input, button')].slice(0, 30);
    const typography = () => elements.map(element => {
      const style = getComputedStyle(element);
      return [style.fontFamily, style.fontWeight, style.fontSize];
    });
    const expected = JSON.stringify(typography());
    try {
      for (const palette of (root.dataset.palettes || '').split(' ').filter(Boolean)) {
        for (const theme of ['light', 'dark']) {
          root.setAttribute('data-palette', palette);
          root.setAttribute('data-theme', theme);
          if (JSON.stringify(typography()) !== expected) return { palette, theme };
        }
      }
      return null;
    } finally {
      for (const [name, value] of original) {
        if (value === null) root.removeAttribute(name); else root.setAttribute(name, value);
      }
    }
  });
  if (result) throw new Error(`Color choices changed typography: ${result.palette} ${result.theme}`);
}
