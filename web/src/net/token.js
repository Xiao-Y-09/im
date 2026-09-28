// token 放 sessionStorage：每个标签页独立登录，同一浏览器里开两个窗口就能测两个用户。
const KEY = 'im_token'

export function getToken() {
  return sessionStorage.getItem(KEY) || ''
}

export function setToken(t) {
  sessionStorage.setItem(KEY, t)
}

export function clearToken() {
  sessionStorage.removeItem(KEY)
}
