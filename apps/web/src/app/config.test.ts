import { expect, it } from 'vitest'
import { artifactURL } from './config'

it('accepts only absolute credential-free Gateway URLs on the configured independent origin', () => {
  expect(artifactURL('http://localhost:8081/artifacts/a/report.html', 'http://localhost:8081', 'http://localhost:5173')?.href).toBe('http://localhost:8081/artifacts/a/report.html')
  for (const url of ['javascript:alert(1)', 'data:text/html,hi', 'blob:http://localhost:8081/a', '/artifacts/a', '//localhost:8081/a', 'http://evil.test/a', 'http://localhost:8081.evil.test/a', 'http://user:pass@localhost:8081/a']) {
    expect(artifactURL(url, 'http://localhost:8081', 'http://localhost:5173')).toBeNull()
  }
  for (const origin of ['', 'null', 'javascript:alert(1)', 'http://localhost:8081/path', 'http://user@localhost:8081', 'http://localhost:8081?x=1']) {
    expect(artifactURL('http://localhost:8081/artifacts/a', origin, 'http://localhost:5173')).toBeNull()
  }
  expect(artifactURL('http://localhost:5173/artifacts/a', 'http://localhost:5173', 'http://localhost:5173')).toBeNull()
})
