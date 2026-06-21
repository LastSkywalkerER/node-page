// copyToClipboard writes text to the clipboard. The async Clipboard API
// (navigator.clipboard) only works in a SECURE context (HTTPS or localhost);
// self-hosted nodes are usually opened over plain HTTP by IP, where it's
// undefined and silently throws — that's why the copy buttons "did nothing".
// Fall back to a hidden <textarea> + execCommand('copy') so copying works on
// HTTP too. Returns true on success.
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Permission denied / not allowed — fall through to the legacy path.
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    // Keep it off-screen and non-disruptive while still selectable.
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    ta.style.left = '0'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}
