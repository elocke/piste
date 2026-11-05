# Performance Guidelines

## Fragment Optimization

- Return **minimal HTML** — only the fragment that changed
- Avoid full layout rendering for htmx requests
- Keep fragments **small and focused** (single responsibility)
- Minimize DOM replacements — target the smallest possible element

## Caching

- **Cache static fragments** with appropriate `Cache-Control` headers
- Use **ETag** headers for conditional requests where applicable
- Set `Cache-Control: no-cache` for dynamic/personalized content
- Use the `preload` extension for predictable navigation

## Request Optimization

- **Debounce** search and filter inputs (`delay:500ms`)
- **Throttle** scroll/resize handlers (`throttle:200ms`)
- Avoid excessive polling — prefer SSE for real-time data
- Use `hx-sync` to prevent duplicate/competing requests
- Use `hx-disabled-elt` to prevent double-submission

## Lazy Loading

```html
<div hx-get="/expensive-content"
     hx-trigger="intersect once"
     hx-target="this"
     hx-swap="outerHTML">
  <div class="skeleton-loader">Loading...</div>
</div>
```

## Server-Side Tips

- Detect `HX-Request` header to skip layout rendering
- Return `204 No Content` when no DOM update is needed
- Use OOB swaps instead of multiple separate requests
- Profile fragment render times — keep them under 50ms
