import { test, expect, type Page } from '@playwright/test';

// Waits until the list has rows and every row's text matches `pattern`
// (an empty list doesn't count, so this can't pass while loading).
async function expectEveryRow(page: Page, pattern: RegExp) {
  await expect
    .poll(async () => {
      const texts = await page.locator('.match-row').allInnerTexts();
      return texts.length > 0 && texts.every((t) => pattern.test(t));
    }, { timeout: 15_000 })
    .toBe(true);
}

test.describe('Matches', () => {
  test('rows show their competition and Load more appends a page', async ({ page }) => {
    await page.goto('/matches');
    const rows = page.locator('.match-row');
    await expect(rows).toHaveCount(20, { timeout: 15_000 });
    await expect(rows.first().locator('.match-row__meta')).toHaveText(/^.+ · .+/);

    await page.getByRole('button', { name: /Load more/ }).click();
    await expect(rows).toHaveCount(40, { timeout: 15_000 });
  });

  test('filters by competition and team, and keeps filters after navigating', async ({ page }) => {
    await page.goto('/matches');
    await expect(page.locator('.match-row').first()).toBeVisible({ timeout: 15_000 });

    await page.getByLabel('Competition').selectOption({ label: 'La Liga 2026/27 (Spain)' });
    await expectEveryRow(page, /La Liga ·/);

    await page.getByLabel('Team').fill('MADRID');
    await expectEveryRow(page, /madrid/i);
    await expect(page).toHaveURL(/team=MADRID/);

    await page.locator('.match-row').first().click();
    await expect(page).toHaveURL(/\/matches\/[0-9a-f-]{36}$/i);
    await page.goBack();
    await expect(page.getByLabel('Team')).toHaveValue('MADRID');
    await expectEveryRow(page, /madrid/i);

    await page.getByRole('button', { name: 'Clear filters' }).click();
    await expect(page).toHaveURL(/\/matches$/);
  });
});
