# Security

## Always

- Use **CSRF tokens** on all state-changing requests (POST, PUT, PATCH, DELETE)
- **Validate on server** — never rely on client-side validation alone
- **Escape HTML output** in templates to prevent XSS
- Use **HTTPS** in production
- **Validate content types** on the server
- Use `hx-disable` on containers with **user-generated content**

## CSRF with FastAPI/Jinja2

```python
# Include CSRF token in forms
<form hx-post="/action">
  <input type="hidden" name="csrf_token" value="{{ csrf_token }}">
  ...
</form>
```

Or via `hx-headers` for all requests:
```html
<body hx-headers='{"X-CSRF-Token": "{{ csrf_token }}"}'>
```

## htmx Security Config

- `htmx.config.selfRequestsOnly` (default: `true`) — restricts AJAX to same domain
- `htmx.config.allowEval` — set to `false` for strict CSP (disables `hx-vals` with `js:` prefix and event filters)
- `htmx.config.inlineScriptNonce` — set CSP nonce for inline scripts
- `htmx.config.inlineStyleNonce` — set CSP nonce for inline styles

## Gotchas

- Never trust `hx-vals` blindly — validate all values on the server
- `hx-vals` with `js:` prefix evaluates JavaScript — sanitize any user input
- `hx-vars` (deprecated) always evaluates as JavaScript — higher XSS risk
