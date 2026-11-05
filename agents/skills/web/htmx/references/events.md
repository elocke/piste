# HTMX Event Lifecycle

## Request Lifecycle Events

| Event | When | Cancelable |
|-------|------|------------|
| `htmx:configRequest` | Before request, modify config | Yes |
| `htmx:beforeRequest` | Just before request is sent | Yes |
| `htmx:afterRequest` | After request completes | No |
| `htmx:beforeSwap` | Before DOM swap | Yes (modify swap behavior) |
| `htmx:afterSwap` | After DOM swap | No |
| `htmx:afterSettle` | After settling phase | No |
| `htmx:afterOnLoad` | After content is loaded and processed | No |

## Error Events

| Event | When |
|-------|------|
| `htmx:responseError` | Server returned error status |
| `htmx:sendError` | Request failed to send |
| `htmx:timeout` | Request timed out |
| `htmx:sseError` | SSE connection error |
| `htmx:wsError` | WebSocket error |

## Other Events

| Event | When |
|-------|------|
| `htmx:confirm` | Before `hx-confirm` dialog |
| `htmx:prompt` | Before `hx-prompt` dialog |
| `htmx:historyRestore` | Restoring from history cache |
| `htmx:load` | New content loaded into DOM |
| `htmx:pushedIntoHistory` | URL pushed to history |
| `htmx:replacedInHistory` | URL replaced in history |
| `htmx:validation:validate` | Before HTML5 validation |
| `htmx:validation:failed` | Validation failed |

## Best Practices

- Use attribute-based solutions (`hx-on`) before writing standalone JavaScript
- Use `hx-on::after-request` for post-request cleanup (form reset, etc.)
- Use `htmx:beforeSwap` to modify swap behavior for specific status codes (e.g., 422)
- Use `htmx:configRequest` to add custom headers or modify parameters
- Events bubble, so you can listen on `document.body` for global handling
