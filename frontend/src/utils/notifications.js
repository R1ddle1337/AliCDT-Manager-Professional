export function notify(text, type = 'success', duration = 4500) {
  window.dispatchEvent(new CustomEvent('alicdt:notify', {
    detail: { text: String(text || ''), type, duration },
  }))
}

export function notifyError(error, fallback) {
  notify(error, 'error', 6500)
}
