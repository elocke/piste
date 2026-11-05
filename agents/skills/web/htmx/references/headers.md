# HTMX Headers

## Request Headers (sent by htmx to server)

| Header | Value | Purpose |
|--------|-------|---------|
| `HX-Request` | `"true"` | Detect htmx requests |
| `HX-Trigger` | Element ID | ID of triggering element |
| `HX-Trigger-Name` | Element name | Name of triggering element |
| `HX-Target` | Element ID | ID of target element |
| `HX-Current-URL` | URL string | Current browser URL |
| `HX-Boosted` | `"true"` | If request is via `hx-boost` |
| `HX-Prompt` | User input | Response to `hx-prompt` dialog |
| `HX-History-Restore-Request` | `"true"` | Restoring from history cache miss |

## Response Headers (sent by server to htmx)

| Header | Purpose |
|--------|---------|
| `HX-Trigger` | Trigger client-side events immediately |
| `HX-Trigger-After-Swap` | Trigger events after DOM swap |
| `HX-Trigger-After-Settle` | Trigger events after settling |
| `HX-Redirect` | Full page redirect |
| `HX-Refresh` | Full page refresh (`true`) |
| `HX-Push-Url` | Push URL to browser history |
| `HX-Replace-Url` | Replace current URL (no history entry) |
| `HX-Location` | Client-side redirect (AJAX, no full reload) |
| `HX-Reswap` | Override swap strategy |
| `HX-Retarget` | Override swap target |
| `HX-Reselect` | Override `hx-select` |

## HX-Trigger with Data

```
HX-Trigger: {"showMessage": {"level": "info", "message": "Saved!"}}
```

Listen for triggered events:
```html
<div hx-trigger="showMessage from:body" hx-get="/notifications">
```

## Best Practices

- Use response headers to coordinate client behavior without custom JS
- Response headers are NOT processed on 3xx redirect responses
- Use `HX-Trigger` for cross-component communication
- Use `HX-Redirect` for post-login redirects
- Use `HX-Push-Url: false` to prevent unwanted history entries
