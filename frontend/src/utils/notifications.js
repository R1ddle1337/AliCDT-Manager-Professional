let lastNotification = { text: '', at: 0 }

export function notify(text, type = 'success', duration = 4500) {
  const normalizedText = String(text || '')
  const now = Date.now()
  if (normalizedText === lastNotification.text && now - lastNotification.at < 1500) return
  lastNotification = { text: normalizedText, at: now }
  window.dispatchEvent(new CustomEvent('alicdt:notify', {
    detail: { text: normalizedText, type, duration },
  }))
}

export function notifyError(text) {
  notify(text, 'error', 6500)
}
