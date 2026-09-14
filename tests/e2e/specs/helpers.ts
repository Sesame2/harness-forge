import { expect, type Page } from '@playwright/test'

// All mutations and reads use the real browser → Go path, never mocked routes.
export async function api(page: Page, path: string, method = 'GET', body?: unknown) {
  return page.evaluate(async ({ path, method, body }) => {
    const response = await fetch(`/api/v1${path}`, { method,
      ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }) })
    return { status: response.status, body: response.status === 204 ? null : await response.json() }
  }, { path, method, body })
}

export async function createGeoProject(page: Page) {
  await page.goto('/')
  await page.getByRole('button', { name: '新建项目', exact: true }).click()
  await page.locator('dialog input').fill(`E2E ${Date.now()}`)
  const created = page.waitForResponse(r => r.url().endsWith('/api/v1/projects') && r.request().method() === 'POST')
  await page.locator('dialog button[type=submit]').click()
  const response = await created; expect(response.status()).toBe(201)
  const project = await response.json()
  await expect(page.getByRole('button', { name: '新建会话', exact: true })).toBeVisible()
  return project.id as string
}

export async function conversation(page: Page) {
  const created = page.waitForResponse(r => /\/projects\/[^/]+\/conversations$/.test(r.url()) && r.request().method() === 'POST')
  await page.getByRole('button', { name: '新建会话', exact: true }).click()
  const response = await created; expect(response.status()).toBe(201)
  const result = await response.json()
  await expect(page.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
  return result.id as string
}

export async function send(page: Page, scenario: string) {
  await page.getByRole('textbox', { name: '消息内容' }).fill(`[fixture:${scenario}]`)
  const created = page.waitForResponse(r => /\/conversations\/[^/]+\/messages$/.test(r.url()) && r.request().method() === 'POST')
  await page.locator('#chat-pane button[type=submit]').click()
  const response = await created; expect(response.status()).toBe(201)
  return (await response.json()).run.id as string
}

export async function finalized(page: Page, id: string, status = 'succeeded') {
  await expect.poll(async () => {
    const result = await api(page, `/runs/${id}`)
    return result.body.finalized_at ? result.body.status : 'not-finalized'
  }).toBe(status)
  const labels = { succeeded: '已完成', failed: '失败', cancelled: '已取消' }
  await expect(page.locator(`[data-run-id="${id}"]`).getByRole('status').first()).toHaveText(labels[status as keyof typeof labels])
}

export async function report(page: Page, heading = 'Geographic report') {
  await expect(page.frameLocator('#artifact-pane iframe').getByRole('heading', { name: heading, exact: true })).toBeVisible()
  const frame = await page.locator('#artifact-pane iframe').elementHandle()
  const content = await frame!.contentFrame()
  expect(await content!.evaluate(() => typeof (window as any).echarts)).toBe('object')
  return (await page.locator('#artifact-pane iframe').getAttribute('src'))!
}

// Read the actual browser-applied Run store (no mutation or substitute API).
export async function handledEvents(page: Page, id: string) {
  return page.evaluate(id => {
    const app = (document.querySelector('#app') as any).__vue_app__
    return (app.config.globalProperties.$pinia._s.get('runs').events.get(id) ?? [])
      .map((event: any) => ({ sequence: event.sequence, type: event.type }))
  }, id)
}
