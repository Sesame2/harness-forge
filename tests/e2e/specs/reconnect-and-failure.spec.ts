import { test, expect } from '@playwright/test'
import { api, conversation, createGeoProject, finalized, handledEvents, report, send } from './helpers'

test('reload inside delayed event gap replays into the UI without gaps or duplicates', async ({ page }) => {
  await createGeoProject(page); await conversation(page)
  const id = await send(page, 'delayed-success')
  await expect(page.locator(`[data-run-id="${id}"] summary`)).toHaveText('Write report 进行中')
  const before = await handledEvents(page, id)
  expect(before.at(-1)?.type).toBe('tool.started')
  const reconnected = page.waitForRequest(r => r.url().endsWith(`/runs/${id}/events/stream`))
  const replayed = page.waitForResponse(r => r.url().endsWith(`/runs/${id}/events?after_sequence=0`))
  await page.reload()
  // The actual reload snapshot still ends at tool.started: reload beat the next event.
  expect((await (await replayed).json()).at(-1).type).toBe('tool.started')
  const connection = await reconnected
  expect(Number(connection.headers()['last-event-id'])).toBeGreaterThanOrEqual(before.at(-1)!.sequence)
  await finalized(page, id)
  const durable = (await api(page, `/runs/${id}/events?after_sequence=0`)).body
    .map((event: any) => ({ sequence: event.sequence, type: event.type }))
  await expect.poll(() => handledEvents(page, id)).toEqual(durable)
  expect(durable.map((event: any) => event.sequence)).toEqual(durable.map((_: unknown, i: number) => i + 1))
  await expect(page.locator(`[data-run-id="${id}"] summary`)).toHaveText('Write report 完成')
  await expect(page.locator('[data-message-role=assistant]')).toHaveCount(1)
  await report(page)
})

test('blocking worker queues cancellation, rejects active deletes and releases on cancel', async ({ page }) => {
  const project = await createGeoProject(page); const convo = await conversation(page)
  const blocking = await send(page, 'blocking')
  await expect.poll(async () => (await api(page, `/runs/${blocking}`)).body.status).toBe('running')
  await expect(page.locator(`[data-run-id="${blocking}"] summary`)).toHaveText('Write report 完成')
  const queued = await send(page, 'geo-report')
  expect((await api(page, `/runs/${queued}`)).body.status).toBe('queued')
  expect((await api(page, `/projects/${project}`, 'DELETE')).status).toBe(409)
  expect((await api(page, `/conversations/${convo}`, 'DELETE')).status).toBe(409)
  await page.getByRole('button', { name: `取消 Run ${queued}`, exact: true }).click()
  await finalized(page, queued, 'cancelled')
  expect((await api(page, `/runs/${blocking}`)).body.status).toBe('running')
  await page.getByRole('button', { name: `取消 Run ${blocking}`, exact: true }).click()
  await finalized(page, blocking, 'cancelled')
  const after = await send(page, 'geo-report')
  await finalized(page, after)
})

test('agent failure and invalid manifest fail without replacing the previous artifact', async ({ page }) => {
  await createGeoProject(page); await conversation(page)
  const good = await send(page, 'geo-report'); await finalized(page, good)
  await expect(page.frameLocator('#artifact-pane iframe').getByRole('heading', { name: 'Geographic report', exact: true })).toBeVisible()
  const original = await page.locator('#artifact-pane iframe').getAttribute('src')
  for (const scenario of ['agent-failure', 'invalid-manifest']) {
    const id = await send(page, scenario); await finalized(page, id, 'failed')
    expect((await api(page, `/runs/${id}/artifacts`)).body).toEqual([])
    await expect(page.getByLabel('制品版本')).toHaveValue(good)
    expect(await report(page)).toBe(original)
  }
})
