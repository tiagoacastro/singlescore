import { test, expect } from '@playwright/test';

test.describe('Dashboard', () => {
  test('renders DEMO badge, headline model metrics, and recent finished matches', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveTitle(/SingleScore/);

    // Primary heading must be present.
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();

    const summary = page.getByRole('region', { name: 'Model summary' });
    await expect(summary).toBeVisible();

    // Both metric cards must land on a non-"—" value. The metric element uses
    // the stable `.metric__value` class; the brier card is the first.
    const metricValues = summary.locator('.metric__value');
    await expect(metricValues).toHaveCount(3);
    await expect(metricValues.nth(0)).not.toHaveText('—', { timeout: 15_000 });
    await expect(metricValues.nth(1)).not.toHaveText('—', { timeout: 15_000 });

    // DEMO chip is present in the page header meta (backend reports demoMode=true).
    await expect(page.locator('.chip', { hasText: 'DEMO' }).first()).toBeVisible();

    // At least one finished match row visible, with its pre-match odds.
    const recentSection = page.getByRole('region', { name: 'Recent matches' });
    await expect(recentSection).toBeVisible();
    await expect(recentSection.locator('.match-row').first()).toBeVisible({ timeout: 15_000 });
    await expect(recentSection.locator('.prob-triplet').first()).toBeVisible();
  });

  test('navigates from a recent match row to the match detail page', async ({ page }) => {
    await page.goto('/');
    const firstRow = page
      .getByRole('region', { name: 'Recent matches' })
      .locator('.match-row')
      .first();
    await expect(firstRow).toBeVisible({ timeout: 15_000 });
    await firstRow.click();

    // URL moves to /matches/<uuid-shaped id>.
    await expect(page).toHaveURL(/\/matches\/[0-9a-f-]{36}$/i);

    // Probability triplet renders.
    await expect(page.locator('.prob-triplet').first()).toBeVisible({ timeout: 15_000 });

    // Probability timeline chart renders at least one data point.
    await expect(page.locator('.chart-wrap svg').first()).toBeVisible({ timeout: 15_000 });
  });
});

test.describe('Evaluation', () => {
  test('renders populated Brier/log-loss headlines and calibration axis', async ({ page }) => {
    await page.goto('/evaluation');
    await expect(page.getByRole('heading', { name: 'Model evaluation', level: 1 })).toBeVisible();

    // Headline cards must land on numeric values (not the "—" fallback).
    const metricValues = page.locator('.metric__value');
    await expect(metricValues).toHaveCount(3);
    await expect(metricValues.nth(0)).not.toHaveText('—', { timeout: 20_000 });
    await expect(metricValues.nth(1)).not.toHaveText('—', { timeout: 20_000 });

    // Calibration chart renders with an X-axis bin label. Recharts animates
    // the bars themselves, so the rectangles can report "hidden" to Playwright
    // for a few frames; the axis labels are static text and are a stable
    // signal that the chart data arrived and painted.
    await expect(page.locator('.chart-wrap svg').first()).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('.chart-wrap svg').first().getByText('50–60%')).toBeVisible({
      timeout: 20_000,
    });

    // The by-minute-bucket table should list several buckets.
    await expect(page.getByRole('cell', { name: 'pre-match' })).toBeVisible({ timeout: 15_000 });
  });
});
