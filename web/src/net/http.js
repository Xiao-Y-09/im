import { getToken, clearToken } from './token.js'

export class HttpError extends Error {
  constructor(status, code, message) {
    super(message)
    this.status = status
    this.code = code
  }
}

// api 自动带 token；401 说明 token 失效，清掉并回登录页。
export async function api(path, { method = 'GET', body, form } = {}) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  let payload
  if (form) {
    payload = form // 让浏览器自己设置 multipart boundary
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }

  const res = await fetch(path, { method, headers, body: payload })
  let data = null
  try {
    data = await res.json()
  } catch {
    // 413 等可能不是 JSON
  }

  if (res.status === 401 && path !== '/api/login') {
    clearToken()
    // 延迟到调用时再取 router，避免 router → views → stores → http → router 的循环依赖
    import('../router.js').then((m) => m.default.push('/login'))
  }
  if (!res.ok) {
    const msg = data?.message || (res.status === 413 ? '文件超过 20MB' : res.statusText)
    throw new HttpError(res.status, data?.error || 'http_error', msg)
  }
  return data
}
