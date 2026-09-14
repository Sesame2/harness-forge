import { test, expect } from '@playwright/test'

test('isolated harness serves the homepage and proxied health', async ({ page, request }) => {
  const response = await request.get('/health')
  expect(response.ok()).toBeTruthy()
  await page.goto('/')
  await expect(page.getByRole('button', { name: '新建项目', exact: true })).toBeVisible()
})
